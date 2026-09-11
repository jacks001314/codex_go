package appserver

import (
	"encoding/base64"
	"strings"
)

// Experimental user-verification APIs for trusted UI clients (Rust #43265,
// #43547, #43925). Go has no native Secure Enclave / biometric provider, so the
// port exposes the exact wire contract and behaves like a Rust build without a
// supported platform provider: status reports `providerUnavailable`, and
// enroll/delete/verify fail with the typed `unavailable` error. The provider
// seam is kept explicit so a future native backend can plug in without wire
// changes.

const (
	userVerificationUnavailableMessage = "User verification is not available in this build or account."
	userVerificationInvalidMessage     = "Invalid verification challenge or display text."

	// maxUserVerificationChallengeEncodedBytes is base64url-no-pad of 4096 bytes.
	maxUserVerificationChallengeEncodedBytes = 5462
	maxUserVerificationChallengeBytes        = 4096
	maxUserVerificationTitleBytes            = 256
	maxUserVerificationDescriptionBytes      = 4096
)

type UserVerificationProof struct {
	CredentialID string `json:"credentialId"`
	Signature    string `json:"signature"`
}

type UserVerificationUnavailableReason string

const (
	UserVerificationUnavailableCredentialMissing     UserVerificationUnavailableReason = "credentialMissing"
	UserVerificationUnavailableBiometricsUnavailable UserVerificationUnavailableReason = "biometricsUnavailable"
	UserVerificationUnavailableProviderUnavailable   UserVerificationUnavailableReason = "providerUnavailable"
)

type UserVerificationCancellationReason string

const (
	UserVerificationCancellationUserCancelled UserVerificationCancellationReason = "userCancelled"
	UserVerificationCancellationInterrupted   UserVerificationCancellationReason = "interrupted"
)

type UserVerificationFailureReason string

const (
	UserVerificationFailureAuthenticationFailed UserVerificationFailureReason = "authenticationFailed"
	UserVerificationFailureTimeout              UserVerificationFailureReason = "timeout"
	UserVerificationFailureProviderError        UserVerificationFailureReason = "providerError"
	UserVerificationFailureServiceError         UserVerificationFailureReason = "serviceError"
)

type UserVerificationInvalidRequestReason string

const UserVerificationInvalidRequestInvalidParams UserVerificationInvalidRequestReason = "invalidParams"

type UserVerificationErrorDetails struct {
	Type   string `json:"type"`
	Reason string `json:"reason"`
}

type UserVerificationStatusParams struct{}

type UserVerificationStatusResponse struct {
	CredentialID       *string                            `json:"credentialId"`
	UnavailableReason  *UserVerificationUnavailableReason `json:"unavailableReason"`
	UnavailableMessage *string                            `json:"unavailableMessage"`
}

type UserVerificationEnrollParams struct{}

// UserVerificationEnrollResponse carries the local credential identity plus,
// since Rust #44877, its public metadata. Algorithm/PublicKey are optional so
// older app-servers that omit them remain compatible; current servers populate
// both. The caller completes backend registration and must check both fields
// before registering.
type UserVerificationEnrollResponse struct {
	CredentialID string `json:"credentialId"`
	// Algorithm is the credential algorithm, `ecdsaP256Sha256X962`.
	Algorithm *string `json:"algorithm"`
	// PublicKey is the unpadded base64url SubjectPublicKeyInfo DER encoding.
	PublicKey *string `json:"publicKey"`
}

type UserVerificationDeleteParams struct{}

type UserVerificationDeleteResponse struct{}

type UserVerificationVerifyParams struct {
	Challenge   string `json:"challenge"`
	Title       string `json:"title"`
	Description string `json:"description"`
}

type UserVerificationVerifyResponse struct {
	Proof UserVerificationProof `json:"proof"`
}

type UserVerificationCancelParams struct {
	RequestID RequestID `json:"requestId"`
}

type UserVerificationCancelResponse struct{}

// userVerificationError carries the typed `data` object Rust attaches to
// user-verification JSON-RPC errors.
type userVerificationError struct {
	code    int
	message string
	data    UserVerificationErrorDetails
	invalid bool
}

func (e *userVerificationError) Error() string {
	if e == nil {
		return ""
	}
	return e.message
}

func (e *userVerificationError) Is(target error) bool {
	// Only invalid-request maps to invalid_params (-32602); every other
	// category falls through to the internal-error default (-32603).
	return e != nil && e.invalid && target == ErrInvalidParams
}

func (e *userVerificationError) JSONRPCErrorData() map[string]any {
	if e == nil {
		return nil
	}
	return map[string]any{"type": e.data.Type, "reason": e.data.Reason}
}

func newUserVerificationError(details UserVerificationErrorDetails, message string, invalid bool) error {
	return &userVerificationError{message: message, data: details, invalid: invalid}
}

func userVerificationUnavailableError() error {
	return newUserVerificationError(
		UserVerificationErrorDetails{Type: "unavailable", Reason: string(UserVerificationUnavailableProviderUnavailable)},
		userVerificationUnavailableMessage,
		false,
	)
}

func userVerificationInvalidError() error {
	return newUserVerificationError(
		UserVerificationErrorDetails{Type: "invalidRequest", Reason: string(UserVerificationInvalidRequestInvalidParams)},
		userVerificationInvalidMessage,
		true,
	)
}

func userVerificationUnavailableStatus() *UserVerificationStatusResponse {
	reason := UserVerificationUnavailableProviderUnavailable
	message := userVerificationUnavailableMessage
	return &UserVerificationStatusResponse{UnavailableReason: &reason, UnavailableMessage: &message}
}

// validateUserVerificationRequest mirrors Rust adapter::validate.
func validateUserVerificationRequest(params *UserVerificationVerifyParams) error {
	if params == nil {
		return userVerificationInvalidError()
	}
	if len(params.Challenge) > maxUserVerificationChallengeEncodedBytes ||
		strings.TrimSpace(params.Title) == "" ||
		len(params.Title) > maxUserVerificationTitleBytes ||
		len(params.Description) > maxUserVerificationDescriptionBytes {
		return userVerificationInvalidError()
	}
	challenge, err := base64.RawURLEncoding.DecodeString(params.Challenge)
	if err != nil || len(challenge) == 0 || len(challenge) > maxUserVerificationChallengeBytes {
		return userVerificationInvalidError()
	}
	return nil
}
