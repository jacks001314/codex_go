//go:build darwin

package appserver

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// Rust 55897425e8 codex-rs/user-verification/src/platform_macos: one
// Secure-Enclave P-256 key per account-user namespace, guarded by a
// biometric access-control policy and a cross-process lifecycle lock. The key
// label is the namespace's sha256, so no account identifier reaches the
// Keychain item name.
const (
	darwinUserVerificationLabelPrefix = "com.openai.codex.user-verification.plugin-service.v1."
	darwinUserVerificationAlgorithm   = "ecdsaP256Sha256X962"
	darwinUserVerificationLockWait    = 60 * time.Second
	darwinUserVerificationLockPoll    = 50 * time.Millisecond
)

// Lock timing is variable so tests can exercise contention without waiting a
// minute; production values mirror Rust's 60 s / 50 ms lifecycle lock.
var (
	darwinUserVerificationLockWaitTime = darwinUserVerificationLockWait
	darwinUserVerificationLockPollTime = darwinUserVerificationLockPoll
	// darwinUserVerificationLockRootOverride keeps tests out of the real home
	// directory; production resolves ~/Library/Application Support.
	darwinUserVerificationLockRootOverride = ""
)

const (
	darwinUserVerificationCredentialMissingMessage = "no user-verification credential has been created"
	darwinUserVerificationInvalidProtectionMessage = "the local credential has incompatible key protection; delete it before enrolling again"
	darwinUserVerificationLockTimeoutMessage       = "timed out waiting for another credential operation"
	darwinUserVerificationLockErrorMessage         = "could not acquire the credential lock"
	darwinUserVerificationInvalidRequestMessage    = "invalid user-verification challenge or display text"
	darwinUserVerificationInvalidPublicKeyMessage  = "could not encode the user-verification public key"
)

// darwinUserVerificationLabel mirrors UserVerificationKeyNamespace::new.
func darwinUserVerificationLabel(accountUserID string) string {
	if accountUserID == "" {
		return ""
	}
	digest := sha256.Sum256([]byte(accountUserID))
	return darwinUserVerificationLabelPrefix + base64.RawURLEncoding.EncodeToString(digest[:])
}

// darwinUserVerificationKeyFromSEC1 mirrors UserVerificationKeyInfo::from_sec1_public_key:
// the platform key's uncompressed P-256 point is validated and wrapped in a
// SubjectPublicKeyInfo before hashing, exactly like the p256 crate's
// to_public_key_der() that Rust hashes.
func darwinUserVerificationKeyFromSEC1(sec1 []byte) (*darwinUserVerificationKey, *darwinUserVerificationError) {
	curve := elliptic.P256()
	x, y := elliptic.Unmarshal(curve, sec1)
	if x == nil {
		return nil, darwinUserVerificationFailed(UserVerificationFailureProviderError, darwinUserVerificationInvalidPublicKeyMessage)
	}
	point := &ecdsa.PublicKey{Curve: curve, X: x, Y: y}
	spki, err := x509.MarshalPKIXPublicKey(point)
	if err != nil {
		return nil, darwinUserVerificationFailed(UserVerificationFailureProviderError, darwinUserVerificationInvalidPublicKeyMessage)
	}
	key := darwinUserVerificationKeyFromSPKI(spki)
	return &key, nil
}

// darwinUserVerificationKeyFromSPKI mirrors UserVerificationKeyInfo: the
// credential id is sha256 of the SubjectPublicKeyInfo DER, base64url without
// padding.
func darwinUserVerificationKeyFromSPKI(spki []byte) darwinUserVerificationKey {
	digest := sha256.Sum256(spki)
	return darwinUserVerificationKey{
		credentialID: base64.RawURLEncoding.EncodeToString(digest[:]),
		publicKey:    base64.RawURLEncoding.EncodeToString(spki),
	}
}

type darwinUserVerificationKey struct {
	credentialID string
	publicKey    string
}

// darwinUserVerificationError is the native error before the app-server
// adapter maps it to its public message; status responses expose Message the
// way Rust returns the provider's own unavailable_message.
type darwinUserVerificationError struct {
	kind    string // unavailable, cancelled, failed
	reason  string
	message string
}

func (e *darwinUserVerificationError) Error() string {
	if e == nil {
		return ""
	}
	return e.message
}

func (e *darwinUserVerificationError) rpcError() error {
	if e == nil {
		return nil
	}
	switch e.kind {
	case "cancelled":
		return userVerificationCancelledError(UserVerificationCancellationReason(e.reason))
	case "unavailable":
		return userVerificationLocalUnavailableError(UserVerificationUnavailableReason(e.reason))
	default:
		return userVerificationFailedError(UserVerificationFailureReason(e.reason))
	}
}

func darwinUserVerificationUnavailable(reason UserVerificationUnavailableReason, message string) *darwinUserVerificationError {
	return &darwinUserVerificationError{kind: "unavailable", reason: string(reason), message: message}
}

func darwinUserVerificationCancelled(reason UserVerificationCancellationReason, message string) *darwinUserVerificationError {
	return &darwinUserVerificationError{kind: "cancelled", reason: string(reason), message: message}
}

func darwinUserVerificationFailed(reason UserVerificationFailureReason, message string) *darwinUserVerificationError {
	return &darwinUserVerificationError{kind: "failed", reason: string(reason), message: message}
}

// classifyDarwinUserVerificationError mirrors platform_macos/error.rs: the
// stable categories clients receive, with OS diagnostics kept out of the wire
// message.
func classifyDarwinUserVerificationError(domain string, code int64) *darwinUserVerificationError {
	switch {
	case (domain == "NSOSStatusErrorDomain" && code == -128) ||
		(domain == "com.apple.LocalAuthentication" && (code == -2 || code == -3)):
		return darwinUserVerificationCancelled(UserVerificationCancellationUserCancelled, "authentication was cancelled")
	case domain == "com.apple.LocalAuthentication" && (code == -4 || code == -9):
		return darwinUserVerificationCancelled(UserVerificationCancellationInterrupted, "authentication was interrupted")
	case (domain == "NSOSStatusErrorDomain" && code == -25293) ||
		(domain == "com.apple.LocalAuthentication" && code == -1):
		return darwinUserVerificationFailed(UserVerificationFailureAuthenticationFailed, "biometric authentication failed")
	case domain == "com.apple.LocalAuthentication" &&
		(code == -5 || code == -6 || code == -7 || code == -8 || code == -12 || code == -13):
		return darwinUserVerificationUnavailable(UserVerificationUnavailableBiometricsUnavailable, "biometric authentication is not available right now")
	case domain == "com.apple.LocalAuthentication" && code == -1004:
		return darwinUserVerificationUnavailable(UserVerificationUnavailableProviderUnavailable, "authentication UI is not permitted in this process")
	case domain == "NSOSStatusErrorDomain" && code == -34018:
		return darwinUserVerificationUnavailable(UserVerificationUnavailableProviderUnavailable, "this binary is missing the required keychain entitlements")
	case domain == "NSOSStatusErrorDomain" && (code == -25291 || code == -25308):
		return darwinUserVerificationUnavailable(UserVerificationUnavailableProviderUnavailable, "the platform keychain is not available right now")
	default:
		return darwinUserVerificationFailed(UserVerificationFailureProviderError, "the platform could not complete user verification")
	}
}

// darwinUserVerificationBackend isolates the Security/LocalAuthentication
// calls so every provider branch is exercisable without hardware.
type darwinUserVerificationBackend interface {
	DeviceSupported() bool
	BiometricsAvailable() *darwinUserVerificationError
	FindKey(label string) (*darwinUserVerificationKey, *darwinUserVerificationError)
	CreateKey(label string) (*darwinUserVerificationKey, *darwinUserVerificationError)
	DeleteKeys(label string) *darwinUserVerificationError
	AuthorizeAndSign(label string, challenge []byte, title, description string, cancelled func() bool) ([]byte, string, *darwinUserVerificationError)
}

type darwinUserVerificationProvider struct {
	backend   darwinUserVerificationBackend
	namespace string
	cancelled func() bool
}

// darwinUserVerificationBindable lets the app-server bind the captured
// account identity and request cancellation without widening the provider
// interface other platforms implement.
type darwinUserVerificationBindable interface {
	BindUserVerificationRequest(accountUserID string, cancelled func() bool) UserVerificationProvider
}

// BindUserVerificationRequest mirrors Rust's per-request
// platform_provider(UserVerificationKeyNamespace::new(identity)): no identity
// means the unsupported provider's unavailable responses.
func (p *darwinUserVerificationProvider) BindUserVerificationRequest(accountUserID string, cancelled func() bool) UserVerificationProvider {
	label := darwinUserVerificationLabel(accountUserID)
	if label == "" {
		return UnsupportedUserVerificationProvider{}
	}
	return &darwinUserVerificationProvider{
		backend:   p.backend,
		namespace: label,
		cancelled: cancelled,
	}
}

// DeviceSupported reports biometric hardware independently of enrollment or
// temporary lockout, like Rust's device_supported().
func (p *darwinUserVerificationProvider) DeviceSupported() bool {
	return p != nil && p.backend != nil && p.backend.DeviceSupported()
}

func (p *darwinUserVerificationProvider) Status() (*UserVerificationStatusResponse, error) {
	if p == nil || p.backend == nil || p.namespace == "" {
		return userVerificationUnavailableStatus(), nil
	}
	release, err := p.lock()
	if err != nil {
		return nil, err.rpcError()
	}
	defer release()
	key, err := p.backend.FindKey(p.namespace)
	if err != nil {
		return nil, err.rpcError()
	}
	if key == nil {
		reason := UserVerificationUnavailableCredentialMissing
		message := darwinUserVerificationCredentialMissingMessage
		return &UserVerificationStatusResponse{UnavailableReason: &reason, UnavailableMessage: &message}, nil
	}
	if bioErr := p.backend.BiometricsAvailable(); bioErr != nil {
		if bioErr.kind == "unavailable" {
			// Rust still reports the existing credential next to the reason:
			// UserVerificationStatus { credential: Some(..), unavailable_reason }.
			reason := UserVerificationUnavailableReason(bioErr.reason)
			message := bioErr.message
			credentialID := key.credentialID
			return &UserVerificationStatusResponse{
				CredentialID:       &credentialID,
				UnavailableReason:  &reason,
				UnavailableMessage: &message,
			}, nil
		}
		return nil, bioErr.rpcError()
	}
	credentialID := key.credentialID
	return &UserVerificationStatusResponse{CredentialID: &credentialID}, nil
}

func (p *darwinUserVerificationProvider) EnsureKey() (*UserVerificationEnrollResponse, error) {
	if p == nil || p.backend == nil || p.namespace == "" {
		return nil, userVerificationUnavailableError()
	}
	release, err := p.lock()
	if err != nil {
		return nil, err.rpcError()
	}
	defer release()
	if p.isCancelled() {
		return nil, darwinUserVerificationCancelled(UserVerificationCancellationInterrupted, "the verification operation is no longer active").rpcError()
	}
	key, findErr := p.backend.FindKey(p.namespace)
	if findErr != nil {
		return nil, findErr.rpcError()
	}
	if key == nil {
		key, findErr = p.backend.CreateKey(p.namespace)
		if findErr != nil {
			return nil, findErr.rpcError()
		}
	}
	algorithm := darwinUserVerificationAlgorithm
	publicKey := key.publicKey
	return &UserVerificationEnrollResponse{
		CredentialID: key.credentialID,
		Algorithm:    &algorithm,
		PublicKey:    &publicKey,
	}, nil
}

func (p *darwinUserVerificationProvider) Delete() (*UserVerificationDeleteResponse, error) {
	if p == nil || p.backend == nil || p.namespace == "" {
		return nil, userVerificationUnavailableError()
	}
	release, err := p.lock()
	if err != nil {
		return nil, err.rpcError()
	}
	defer release()
	// The credential id is optional; a lookup failure must not prevent removing
	// this namespace's key items, matching Rust's delete.
	_, _ = p.backend.FindKey(p.namespace)
	if deleteErr := p.backend.DeleteKeys(p.namespace); deleteErr != nil {
		return nil, deleteErr.rpcError()
	}
	return &UserVerificationDeleteResponse{}, nil
}

func (p *darwinUserVerificationProvider) Verify(params UserVerificationVerifyParams) (*UserVerificationVerifyResponse, error) {
	if p == nil || p.backend == nil || p.namespace == "" {
		return nil, userVerificationUnavailableError()
	}
	if len(params.Challenge) > maxUserVerificationChallengeEncodedBytes ||
		params.Title == "" ||
		len(params.Title) > maxUserVerificationTitleBytes ||
		len(params.Description) > maxUserVerificationDescriptionBytes {
		native := &darwinUserVerificationError{
			kind:    "failed",
			reason:  string(UserVerificationFailureProviderError),
			message: darwinUserVerificationInvalidRequestMessage,
		}
		return nil, native.rpcError()
	}
	challenge, err := base64.RawURLEncoding.DecodeString(params.Challenge)
	if err != nil || len(challenge) == 0 || len(challenge) > maxUserVerificationChallengeBytes {
		native := &darwinUserVerificationError{
			kind:    "failed",
			reason:  string(UserVerificationFailureProviderError),
			message: darwinUserVerificationInvalidRequestMessage,
		}
		return nil, native.rpcError()
	}
	release, lockErr := p.lock()
	if lockErr != nil {
		return nil, lockErr.rpcError()
	}
	defer release()
	signature, credentialID, signErr := p.backend.AuthorizeAndSign(
		p.namespace, challenge, params.Title, params.Description, p.isCancelled,
	)
	if signErr != nil {
		return nil, signErr.rpcError()
	}
	return &UserVerificationVerifyResponse{
		Proof: UserVerificationProof{
			CredentialID: credentialID,
			Signature:    base64.RawURLEncoding.EncodeToString(signature),
		},
	}, nil
}

func (p *darwinUserVerificationProvider) isCancelled() bool {
	return p != nil && p.cancelled != nil && p.cancelled()
}

// lock takes Rust's cross-process lifecycle lock:
// ~/Library/Application Support/com.openai.codex/user-verification/<label>.lock,
// polled every 50 ms for up to 60 s and abandoned as soon as the request is
// cancelled.
func (p *darwinUserVerificationProvider) lock() (func(), *darwinUserVerificationError) {
	directory, err := darwinUserVerificationLockDirectory()
	if err != nil {
		return nil, darwinUserVerificationFailed(UserVerificationFailureProviderError, darwinUserVerificationLockErrorMessage)
	}
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return nil, darwinUserVerificationFailed(UserVerificationFailureProviderError, darwinUserVerificationLockErrorMessage)
	}
	file, err := os.OpenFile(filepath.Join(directory, p.namespace+".lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, darwinUserVerificationFailed(UserVerificationFailureProviderError, darwinUserVerificationLockErrorMessage)
	}
	deadline := time.Now().Add(darwinUserVerificationLockWaitTime)
	for {
		if p.isCancelled() {
			file.Close()
			return nil, darwinUserVerificationCancelled(UserVerificationCancellationInterrupted, "the verification operation is no longer active")
		}
		err := unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB)
		if err == nil {
			return func() {
				_ = unix.Flock(int(file.Fd()), unix.LOCK_UN)
				_ = file.Close()
			}, nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EAGAIN) {
			file.Close()
			return nil, darwinUserVerificationFailed(UserVerificationFailureProviderError, darwinUserVerificationLockErrorMessage)
		}
		if time.Now().After(deadline) {
			file.Close()
			return nil, darwinUserVerificationFailed(UserVerificationFailureTimeout, darwinUserVerificationLockTimeoutMessage)
		}
		time.Sleep(darwinUserVerificationLockPollTime)
	}
}

func darwinUserVerificationLockDirectory() (string, error) {
	if darwinUserVerificationLockRootOverride != "" {
		return darwinUserVerificationLockRootOverride, nil
	}
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return "", errors.New("home directory unavailable")
	}
	return filepath.Join(home, "Library", "Application Support", "com.openai.codex", "user-verification"), nil
}
