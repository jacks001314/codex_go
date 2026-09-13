package appserver

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"sort"
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

// UnmarshalJSON mirrors Rust's `#[serde(deny_unknown_fields)]` on the empty
// status params.
func (p *UserVerificationStatusParams) UnmarshalJSON(data []byte) error {
	_, err := emptyUserVerificationParams(data)
	return err
}

type UserVerificationStatusResponse struct {
	CredentialID       *string                            `json:"credentialId"`
	UnavailableReason  *UserVerificationUnavailableReason `json:"unavailableReason"`
	UnavailableMessage *string                            `json:"unavailableMessage"`
}

type UserVerificationEnrollParams struct{}

// UnmarshalJSON mirrors Rust's `#[serde(deny_unknown_fields)]` on the empty
// enroll params.
func (p *UserVerificationEnrollParams) UnmarshalJSON(data []byte) error {
	_, err := emptyUserVerificationParams(data)
	return err
}

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

// UnmarshalJSON mirrors Rust's `#[serde(deny_unknown_fields)]` on the empty
// delete params.
func (p *UserVerificationDeleteParams) UnmarshalJSON(data []byte) error {
	_, err := emptyUserVerificationParams(data)
	return err
}

type UserVerificationDeleteResponse struct{}

type UserVerificationVerifyParams struct {
	Challenge   string `json:"challenge"`
	Title       string `json:"title"`
	Description string `json:"description"`
}

// UnmarshalJSON mirrors Rust's `#[serde(deny_unknown_fields)]` verify params:
// all three fields are required (Rust has no serde defaults) and unknown fields
// are rejected. The value validation stays in validateUserVerificationRequest.
func (p *UserVerificationVerifyParams) UnmarshalJSON(data []byte) error {
	fields, err := userVerificationParamFields(data)
	if err != nil {
		return err
	}
	if err := rejectUnknownUserVerificationParams(fields, "challenge", "title", "description"); err != nil {
		return err
	}
	if p.Challenge, err = requiredUserVerificationString(fields, "challenge"); err != nil {
		return err
	}
	if p.Title, err = requiredUserVerificationString(fields, "title"); err != nil {
		return err
	}
	if p.Description, err = requiredUserVerificationString(fields, "description"); err != nil {
		return err
	}
	return nil
}

type UserVerificationVerifyResponse struct {
	Proof UserVerificationProof `json:"proof"`
}

type UserVerificationCancelParams struct {
	RequestID RequestID `json:"requestId"`
}

// UnmarshalJSON mirrors Rust's `#[serde(deny_unknown_fields)]` cancel params: the
// object must contain exactly a non-null `requestId` (Rust's RequestId is an
// untagged integer-or-string, so null or an extra field is an invalid request).
func (p *UserVerificationCancelParams) UnmarshalJSON(data []byte) error {
	fields, err := userVerificationParamFields(data)
	if err != nil {
		return err
	}
	if err := rejectUnknownUserVerificationParams(fields, "requestId"); err != nil {
		return err
	}
	raw, ok := fields["requestId"]
	if !ok {
		return fmt.Errorf("userVerification/cancel params are missing requestId")
	}
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return fmt.Errorf("userVerification/cancel requestId must be an integer or string")
	}
	return json.Unmarshal(raw, &p.RequestID)
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

// userVerificationParamFields decodes a params object, mirroring Rust's
// `deny_unknown_fields` shape check: an explicit null or a non-object params
// value cannot deserialize into the params struct.
func userVerificationParamFields(data []byte) (map[string]json.RawMessage, error) {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil, fmt.Errorf("userVerification params must be a JSON object")
	}
	fields := map[string]json.RawMessage{}
	if err := json.Unmarshal(trimmed, &fields); err != nil {
		return nil, err
	}
	return fields, nil
}

// emptyUserVerificationParams accepts only an empty params object.
func emptyUserVerificationParams(data []byte) (map[string]json.RawMessage, error) {
	fields, err := userVerificationParamFields(data)
	if err != nil {
		return nil, err
	}
	if len(fields) != 0 {
		return nil, fmt.Errorf("unknown userVerification params: %s", strings.Join(sortedUserVerificationKeys(fields), ", "))
	}
	return fields, nil
}

// rejectUnknownUserVerificationParams rejects any field outside allowed.
func rejectUnknownUserVerificationParams(fields map[string]json.RawMessage, allowed ...string) error {
	allowedSet := make(map[string]bool, len(allowed))
	for _, key := range allowed {
		allowedSet[key] = true
	}
	var unknown []string
	for key := range fields {
		if !allowedSet[key] {
			unknown = append(unknown, key)
		}
	}
	if len(unknown) != 0 {
		sort.Strings(unknown)
		return fmt.Errorf("unknown userVerification params: %s", strings.Join(unknown, ", "))
	}
	return nil
}

// requiredUserVerificationString reads a required string field.
func requiredUserVerificationString(fields map[string]json.RawMessage, key string) (string, error) {
	raw, ok := fields[key]
	if !ok {
		return "", fmt.Errorf("userVerification params are missing %s", key)
	}
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return "", fmt.Errorf("userVerification %s must be a string", key)
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", err
	}
	return value, nil
}

func sortedUserVerificationKeys(fields map[string]json.RawMessage) []string {
	keys := make([]string, 0, len(fields))
	for key := range fields {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
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
