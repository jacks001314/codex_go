//go:build darwin

package appserver

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"codex_go/auth"

	"golang.org/x/sys/unix"
)

func TestDarwinUserVerificationLabelMatchesRustNamespace(t *testing.T) {
	if got := darwinUserVerificationLabel(""); got != "" {
		t.Fatalf("empty identity label = %q, want empty", got)
	}
	label := darwinUserVerificationLabel("account-user-a")
	want := darwinUserVerificationLabelPrefix + base64.RawURLEncoding.EncodeToString(sha256Bytes("account-user-a"))
	if label != want {
		t.Fatalf("label = %q, want %q", label, want)
	}
	if darwinUserVerificationLabel("account-user-b") == label {
		t.Fatal("different account users must use different labels")
	}
}

func sha256Bytes(value string) []byte {
	sum := sha256.Sum256([]byte(value))
	return sum[:]
}

func TestDarwinUserVerificationErrorClassificationMatchesRust(t *testing.T) {
	for _, tc := range []struct {
		domain  string
		code    int64
		kind    string
		reason  string
		message string
	}{
		{"NSOSStatusErrorDomain", -128, "cancelled", "userCancelled", "authentication was cancelled"},
		{"com.apple.LocalAuthentication", -2, "cancelled", "userCancelled", "authentication was cancelled"},
		{"com.apple.LocalAuthentication", -3, "cancelled", "userCancelled", "authentication was cancelled"},
		{"com.apple.LocalAuthentication", -4, "cancelled", "interrupted", "authentication was interrupted"},
		{"com.apple.LocalAuthentication", -9, "cancelled", "interrupted", "authentication was interrupted"},
		{"NSOSStatusErrorDomain", -25293, "failed", "authenticationFailed", "biometric authentication failed"},
		{"com.apple.LocalAuthentication", -1, "failed", "authenticationFailed", "biometric authentication failed"},
		{"com.apple.LocalAuthentication", -5, "unavailable", "biometricsUnavailable", "biometric authentication is not available right now"},
		{"com.apple.LocalAuthentication", -8, "unavailable", "biometricsUnavailable", "biometric authentication is not available right now"},
		{"com.apple.LocalAuthentication", -13, "unavailable", "biometricsUnavailable", "biometric authentication is not available right now"},
		{"com.apple.LocalAuthentication", -1004, "unavailable", "providerUnavailable", "authentication UI is not permitted in this process"},
		{"NSOSStatusErrorDomain", -34018, "unavailable", "providerUnavailable", "this binary is missing the required keychain entitlements"},
		{"NSOSStatusErrorDomain", -25291, "unavailable", "providerUnavailable", "the platform keychain is not available right now"},
		{"NSOSStatusErrorDomain", -25308, "unavailable", "providerUnavailable", "the platform keychain is not available right now"},
		{"NSOSStatusErrorDomain", -50, "failed", "providerError", "the platform could not complete user verification"},
		{"com.apple.LocalAuthentication", -1000, "failed", "providerError", "the platform could not complete user verification"},
	} {
		got := classifyDarwinUserVerificationError(tc.domain, tc.code)
		if got.kind != tc.kind || got.reason != tc.reason || got.message != tc.message {
			t.Errorf("classify(%s, %d) = %+v, want %s/%s/%s", tc.domain, tc.code, got, tc.kind, tc.reason, tc.message)
		}
	}
}

func TestDarwinUserVerificationRPCErrorMessagesMatchAdapter(t *testing.T) {
	for _, tc := range []struct {
		native  *darwinUserVerificationError
		kind    string
		reason  string
		message string
	}{
		{darwinUserVerificationUnavailable(UserVerificationUnavailableCredentialMissing, "missing"), "unavailable", "credentialMissing", userVerificationLocalUnavailableMessage},
		{darwinUserVerificationCancelled(UserVerificationCancellationUserCancelled, "cancelled"), "cancelled", "userCancelled", userVerificationCancelledMessage},
		{darwinUserVerificationFailed(UserVerificationFailureProviderError, "failed"), "failed", "providerError", userVerificationFailedMessage},
	} {
		err, ok := tc.native.rpcError().(*userVerificationError)
		if !ok {
			t.Fatalf("rpcError type = %T", tc.native.rpcError())
		}
		if err.data.Type != tc.kind || err.data.Reason != tc.reason || err.message != tc.message || err.invalid {
			t.Fatalf("rpcError = %+v, want %s/%s/%s", err, tc.kind, tc.reason, tc.message)
		}
	}
}

func TestDarwinUserVerificationProviderStatusBranches(t *testing.T) {
	withTestLockDirectory(t)
	backend := &fakeDarwinUVBackend{}
	provider := &darwinUserVerificationProvider{backend: backend, namespace: darwinUserVerificationLabel("account-user-a")}

	status, err := provider.Status()
	if err != nil {
		t.Fatalf("missing credential status error: %+v", err)
	}
	if status.CredentialID != nil || status.UnavailableReason == nil || *status.UnavailableReason != UserVerificationUnavailableCredentialMissing {
		t.Fatalf("missing credential status = %+v", status)
	}
	if status.UnavailableMessage == nil || *status.UnavailableMessage != darwinUserVerificationCredentialMissingMessage {
		t.Fatalf("missing credential message = %+v", status.UnavailableMessage)
	}

	backend.key = &darwinUserVerificationKey{credentialID: "credential-1", publicKey: "public-1"}
	backend.bioErr = darwinUserVerificationUnavailable(UserVerificationUnavailableBiometricsUnavailable, "biometric authentication is not available right now")
	status, err = provider.Status()
	if err != nil {
		t.Fatalf("biometrics unavailable status error: %+v", err)
	}
	// Rust's status carries the existing credential next to the reason
	// (UserVerificationStatus { credential: Some(..), unavailable_reason }), so
	// the app-server response reports both fields at once.
	if status.CredentialID == nil || *status.CredentialID != "credential-1" || status.UnavailableReason == nil ||
		*status.UnavailableReason != UserVerificationUnavailableBiometricsUnavailable ||
		status.UnavailableMessage == nil || *status.UnavailableMessage != "biometric authentication is not available right now" {
		t.Fatalf("biometrics unavailable status = %+v", status)
	}

	backend.bioErr = nil
	status, err = provider.Status()
	if err != nil || status.CredentialID == nil || *status.CredentialID != "credential-1" || status.UnavailableReason != nil {
		t.Fatalf("ready status = %+v, err %+v", status, err)
	}

	backend.findErr = darwinUserVerificationUnavailable(UserVerificationUnavailableProviderUnavailable, darwinUserVerificationInvalidProtectionMessage)
	if _, err := provider.Status(); err == nil || err.Error() != userVerificationLocalUnavailableMessage {
		t.Fatalf("incompatible protection error = %+v", err)
	}
}

// TestDarwinUserVerificationKeyInfoMatchesRustP256Derivation checks the
// credential metadata without hardware: Rust hashes the SubjectPublicKeyInfo DER
// that p256's to_public_key_der() emits for the SEC1 point returned by
// SecKeyCopyExternalRepresentation.
func TestDarwinUserVerificationKeyInfoMatchesRustP256Derivation(t *testing.T) {
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	sec1 := elliptic.Marshal(elliptic.P256(), privateKey.X, privateKey.Y)
	if len(sec1) != 65 || sec1[0] != 0x04 {
		t.Fatalf("SEC1 point = %d bytes starting 0x%02x", len(sec1), sec1[0])
	}
	spki, err := x509.MarshalPKIXPublicKey(&privateKey.PublicKey)
	if err != nil {
		t.Fatalf("marshal SPKI: %v", err)
	}
	// The DER header must be the fixed 26-byte P-256 SubjectPublicKeyInfo prefix
	// Rust's p256 crate emits, followed by the uncompressed point.
	wantPrefix := []byte{
		0x30, 0x59, 0x30, 0x13, 0x06, 0x07, 0x2a, 0x86, 0x48, 0xce, 0x3d, 0x02, 0x01,
		0x06, 0x08, 0x2a, 0x86, 0x48, 0xce, 0x3d, 0x03, 0x01, 0x07, 0x03, 0x42, 0x00,
	}
	if len(spki) != len(wantPrefix)+65 || !bytes.Equal(spki[:len(wantPrefix)], wantPrefix) {
		t.Fatalf("SPKI = %x", spki)
	}

	key, keyErr := darwinUserVerificationKeyFromSEC1(sec1)
	if keyErr != nil {
		t.Fatalf("derive key: %+v", keyErr)
	}
	if want := base64.RawURLEncoding.EncodeToString(spki); key.publicKey != want {
		t.Fatalf("publicKey = %q, want %q", key.publicKey, want)
	}
	digest := sha256.Sum256(spki)
	if want := base64.RawURLEncoding.EncodeToString(digest[:]); key.credentialID != want {
		t.Fatalf("credentialId = %q, want %q", key.credentialID, want)
	}

	if _, invalid := darwinUserVerificationKeyFromSEC1([]byte{0x04, 0x01, 0x02}); invalid == nil ||
		invalid.message != darwinUserVerificationInvalidPublicKeyMessage ||
		invalid.kind != "failed" || invalid.reason != string(UserVerificationFailureProviderError) {
		t.Fatalf("invalid SEC1 = %+v", invalid)
	}
}

func TestDarwinUserVerificationProviderEnrollDeleteBranches(t *testing.T) {
	withTestLockDirectory(t)
	backend := &fakeDarwinUVBackend{key: &darwinUserVerificationKey{credentialID: "existing", publicKey: "public-existing"}}
	provider := &darwinUserVerificationProvider{backend: backend, namespace: darwinUserVerificationLabel("account-user-a")}

	enroll, err := provider.EnsureKey()
	if err != nil {
		t.Fatalf("existing enroll error: %+v", err)
	}
	if enroll.CredentialID != "existing" || backend.created != 0 {
		t.Fatalf("existing enroll = %+v, created %d", enroll, backend.created)
	}
	if enroll.Algorithm == nil || *enroll.Algorithm != darwinUserVerificationAlgorithm ||
		enroll.PublicKey == nil || *enroll.PublicKey != "public-existing" {
		t.Fatalf("existing enroll metadata = %+v", enroll)
	}

	backend.key = nil
	backend.createdKey = &darwinUserVerificationKey{credentialID: "created", publicKey: "public-created"}
	enroll, err = provider.EnsureKey()
	if err != nil {
		t.Fatalf("created enroll error: %+v", err)
	}
	if enroll.CredentialID != "created" || backend.created != 1 {
		t.Fatalf("created enroll = %+v, created %d", enroll, backend.created)
	}

	if _, err := provider.Delete(); err != nil {
		t.Fatalf("delete error: %+v", err)
	}
	if backend.deleted != 1 {
		t.Fatalf("deleted = %d, want 1", backend.deleted)
	}
	backend.deleteErr = darwinUserVerificationFailed(UserVerificationFailureProviderError, "boom")
	if _, err := provider.Delete(); err == nil || err.Error() != userVerificationFailedMessage {
		t.Fatalf("delete failure error = %+v", err)
	}
}

func TestDarwinUserVerificationProviderVerifyBranches(t *testing.T) {
	withTestLockDirectory(t)
	challenge := base64.RawURLEncoding.EncodeToString([]byte("challenge-bytes"))
	backend := &fakeDarwinUVBackend{signature: []byte{1, 2, 3}, signCredential: "credential-1"}
	provider := &darwinUserVerificationProvider{backend: backend, namespace: darwinUserVerificationLabel("account-user-a")}

	proof, err := provider.Verify(UserVerificationVerifyParams{Challenge: challenge, Title: "Approve", Description: "Confirm"})
	if err != nil {
		t.Fatalf("verify error: %+v", err)
	}
	if proof.Proof.CredentialID != "credential-1" || proof.Proof.Signature != base64.RawURLEncoding.EncodeToString([]byte{1, 2, 3}) {
		t.Fatalf("proof = %+v", proof.Proof)
	}
	if backend.lastChallenge != "challenge-bytes" || backend.lastTitle != "Approve" || backend.lastDescription != "Confirm" {
		t.Fatalf("backend request = %q/%q/%q", backend.lastChallenge, backend.lastTitle, backend.lastDescription)
	}

	if _, err := provider.Verify(UserVerificationVerifyParams{Challenge: "not-base64!!", Title: "Approve"}); err == nil || err.Error() != userVerificationFailedMessage {
		t.Fatalf("invalid verify error = %+v", err)
	}

	backend.signErr = darwinUserVerificationCancelled(UserVerificationCancellationUserCancelled, "cancelled")
	if _, err := provider.Verify(UserVerificationVerifyParams{Challenge: challenge, Title: "Approve"}); err == nil || err.Error() != userVerificationCancelledMessage {
		t.Fatalf("cancelled verify error = %+v", err)
	}
}

func TestDarwinUserVerificationProviderBindRequiresIdentity(t *testing.T) {
	backend := &fakeDarwinUVBackend{}
	base := &darwinUserVerificationProvider{backend: backend}
	if bound := base.BindUserVerificationRequest("", nil); bound != (UnsupportedUserVerificationProvider{}) {
		t.Fatalf("empty identity bound provider = %T, want unsupported", bound)
	}
	bound := base.BindUserVerificationRequest("account-user-a", nil)
	provider, ok := bound.(*darwinUserVerificationProvider)
	if !ok {
		t.Fatalf("bound provider type = %T", bound)
	}
	if provider.namespace != darwinUserVerificationLabel("account-user-a") {
		t.Fatalf("bound namespace = %q", provider.namespace)
	}
}

func TestDarwinUserVerificationAccountIdentityMatchesRust(t *testing.T) {
	jwt := "eyJhbGciOiJub25lIn0." + base64.RawURLEncoding.EncodeToString([]byte(
		`{"chatgpt_account_id":"workspace-a","chatgpt_account_user_id":"account-user-a"}`)) + "."
	manager := auth.NewAccountManager()
	manager.ApplyAuthSnapshot(&auth.AuthDotJSON{
		AuthMode: "chatgpt",
		Tokens: map[string]any{
			"account_id":   "workspace-a",
			"access_token": jwt,
		},
	})
	if got := userVerificationAccountUserID(manager); got != "account-user-a" {
		t.Fatalf("identity = %q, want account-user-a", got)
	}
	manager.ApplyAuthSnapshot(&auth.AuthDotJSON{AuthMode: "chatgpt", Tokens: map[string]any{"account_id": "workspace-b", "access_token": jwt}})
	if got := userVerificationAccountUserID(manager); got != "" {
		t.Fatalf("other-workspace identity = %q, want empty", got)
	}
}

func TestDarwinUserVerificationServiceBindsIdentityAndCancels(t *testing.T) {
	withTestLockDirectory(t)
	backend := &fakeDarwinUVBackend{}
	provider := &darwinUserVerificationProvider{backend: backend}
	service := &userVerificationService{provider: provider}

	params, _ := json.Marshal(UserVerificationStatusParams{})
	response, err := service.handle(&Request{ID: IntID(1), Method: MethodUserVerificationStatus, Params: params}, "stdio")
	if err != nil {
		t.Fatalf("status without account error: %+v", err)
	}
	status := response.(*UserVerificationStatusResponse)
	if status.UnavailableReason == nil || *status.UnavailableReason != UserVerificationUnavailableProviderUnavailable {
		t.Fatalf("status without account = %+v", status)
	}

	jwt := "eyJhbGciOiJub25lIn0." + base64.RawURLEncoding.EncodeToString([]byte(
		`{"chatgpt_account_id":"workspace-a","chatgpt_account_user_id":"account-user-a"}`)) + "."
	manager := auth.NewAccountManager()
	manager.ApplyAuthSnapshot(&auth.AuthDotJSON{
		AuthMode: "chatgpt",
		Tokens:   map[string]any{"account_id": "workspace-a", "access_token": jwt},
	})
	service.account = manager
	response, err = service.handle(&Request{ID: IntID(1), Method: MethodUserVerificationStatus, Params: params}, "stdio")
	if err != nil {
		t.Fatalf("bound status error: %+v", err)
	}
	status = response.(*UserVerificationStatusResponse)
	if status.UnavailableReason == nil || *status.UnavailableReason != UserVerificationUnavailableCredentialMissing {
		t.Fatalf("bound status = %+v", status)
	}
	if backend.lastLabel != darwinUserVerificationLabel("account-user-a") {
		t.Fatalf("backend label = %q", backend.lastLabel)
	}

	token := &userVerificationCancelToken{}
	binding := service.providerForRequest(token.IsCancelled)
	if provider, ok := binding.(*darwinUserVerificationProvider); ok {
		if provider.isCancelled() {
			t.Fatal("fresh request must not start cancelled")
		}
		token.Cancel()
		if !provider.isCancelled() {
			t.Fatal("cancelled token must reach the provider")
		}
	}
}

func TestDarwinUserVerificationLockTimeoutAndCancellation(t *testing.T) {
	directory := withTestLockDirectory(t)
	label := darwinUserVerificationLabel("account-user-a")
	file, err := os.OpenFile(filepath.Join(directory, label+".lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatalf("open lock: %v", err)
	}
	defer file.Close()
	if err := unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		t.Fatalf("hold lock: %v", err)
	}
	defer unix.Flock(int(file.Fd()), unix.LOCK_UN)

	restoreWait, restorePoll := darwinUserVerificationLockWaitTime, darwinUserVerificationLockPollTime
	darwinUserVerificationLockWaitTime = 60 * time.Millisecond
	darwinUserVerificationLockPollTime = 5 * time.Millisecond
	defer func() {
		darwinUserVerificationLockWaitTime = restoreWait
		darwinUserVerificationLockPollTime = restorePoll
	}()

	provider := &darwinUserVerificationProvider{backend: &fakeDarwinUVBackend{}, namespace: label}
	if _, err := provider.lock(); err == nil || err.reason != string(UserVerificationFailureTimeout) {
		t.Fatalf("lock timeout = %+v", err)
	}

	cancelled := true
	provider.cancelled = func() bool { return cancelled }
	if _, err := provider.lock(); err == nil || err.reason != string(UserVerificationCancellationInterrupted) {
		t.Fatalf("lock cancellation = %+v", err)
	}
}

func withTestLockDirectory(t *testing.T) string {
	t.Helper()
	directory := t.TempDir()
	previous := darwinUserVerificationLockRootOverride
	darwinUserVerificationLockRootOverride = directory
	t.Cleanup(func() { darwinUserVerificationLockRootOverride = previous })
	return directory
}

// fakeDarwinUVBackend records calls and returns configured results so every
// provider branch is covered without Secure Enclave hardware.
type fakeDarwinUVBackend struct {
	device          bool
	bioErr          *darwinUserVerificationError
	key             *darwinUserVerificationKey
	findErr         *darwinUserVerificationError
	createdKey      *darwinUserVerificationKey
	createErr       *darwinUserVerificationError
	deleted         int
	deleteErr       *darwinUserVerificationError
	signature       []byte
	signCredential  string
	signErr         *darwinUserVerificationError
	lastLabel       string
	lastChallenge   string
	lastTitle       string
	lastDescription string
	created         int
}

func (b *fakeDarwinUVBackend) DeviceSupported() bool { return b.device }

func (b *fakeDarwinUVBackend) BiometricsAvailable() *darwinUserVerificationError { return b.bioErr }

func (b *fakeDarwinUVBackend) FindKey(label string) (*darwinUserVerificationKey, *darwinUserVerificationError) {
	b.lastLabel = label
	if b.findErr != nil {
		return nil, b.findErr
	}
	if b.key == nil {
		return nil, nil
	}
	key := *b.key
	return &key, nil
}

func (b *fakeDarwinUVBackend) CreateKey(label string) (*darwinUserVerificationKey, *darwinUserVerificationError) {
	b.lastLabel = label
	b.created++
	if b.createErr != nil {
		return nil, b.createErr
	}
	if b.createdKey == nil {
		return nil, darwinUserVerificationFailed(UserVerificationFailureProviderError, "fake backend has no created key")
	}
	key := *b.createdKey
	return &key, nil
}

func (b *fakeDarwinUVBackend) DeleteKeys(label string) *darwinUserVerificationError {
	b.lastLabel = label
	b.deleted++
	return b.deleteErr
}

func (b *fakeDarwinUVBackend) AuthorizeAndSign(label string, challenge []byte, title, description string, cancelled func() bool) ([]byte, string, *darwinUserVerificationError) {
	b.lastLabel = label
	b.lastChallenge = string(challenge)
	b.lastTitle = title
	b.lastDescription = description
	if b.signErr != nil {
		return nil, "", b.signErr
	}
	return b.signature, b.signCredential, nil
}
