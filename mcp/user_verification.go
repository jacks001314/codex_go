package mcp

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"strings"
)

// UserVerificationReasonKey is the bounded metadata key carrying a user
// verification diagnostic category.
const UserVerificationReasonKey = "openai/userVerificationReason"

// UserVerificationMode is the OpenAI elicitation mode that requests native
// user verification (Rust's user_verification::MODE).
const UserVerificationMode = "openai/userVerification"

const (
	maxUserVerificationTitleBytes       = 256
	maxUserVerificationDescriptionBytes = 4096
	maxUserVerificationChallengeBytes   = 4096
)

// UserVerificationReason is a closed diagnostic category. It never carries a
// proof, display message, or raw provider error.
type UserVerificationReason string

const (
	UserVerificationReasonUserCancelled         UserVerificationReason = "userCancelled"
	UserVerificationReasonUserDeclined          UserVerificationReason = "userDeclined"
	UserVerificationReasonCredentialMissing     UserVerificationReason = "credentialMissing"
	UserVerificationReasonBiometricsUnavailable UserVerificationReason = "biometricsUnavailable"
	UserVerificationReasonProviderUnavailable   UserVerificationReason = "providerUnavailable"
	UserVerificationReasonAuthenticationFailed  UserVerificationReason = "authenticationFailed"
	UserVerificationReasonTimeout               UserVerificationReason = "timeout"
	UserVerificationReasonProviderError         UserVerificationReason = "providerError"
	UserVerificationReasonServiceError          UserVerificationReason = "serviceError"
	UserVerificationReasonInvalidParams         UserVerificationReason = "invalidParams"
	UserVerificationReasonInvalidProof          UserVerificationReason = "invalidProof"
	UserVerificationReasonInvalidResponse       UserVerificationReason = "invalidResponse"
	UserVerificationReasonResponseError         UserVerificationReason = "responseError"
	UserVerificationReasonResponseChannelClosed UserVerificationReason = "responseChannelClosed"
	UserVerificationReasonInterrupted           UserVerificationReason = "interrupted"
	UserVerificationReasonApprovalUnavailable   UserVerificationReason = "approvalUnavailable"
)

// UserVerificationReasonFromMeta returns a recognized reason and discards
// unknown metadata values.
func UserVerificationReasonFromMeta(meta any) (UserVerificationReason, bool) {
	values, ok := meta.(map[string]any)
	if !ok {
		return "", false
	}
	text, ok := values[UserVerificationReasonKey].(string)
	if !ok {
		return "", false
	}
	reason := UserVerificationReason(strings.TrimSpace(text))
	if !reason.valid() {
		return "", false
	}
	return reason, true
}

// UserVerificationReasonMeta builds the only metadata accepted for a user
// verification failure, decline, or cancellation.
func UserVerificationReasonMeta(reason UserVerificationReason) map[string]any {
	if !reason.valid() {
		return nil
	}
	return map[string]any{UserVerificationReasonKey: string(reason)}
}

// NormalizeUserVerificationResponse validates proof material for acceptance and
// preserves only a bounded diagnostic reason for declines and cancellations.
func NormalizeUserVerificationResponse(response *MCPElicitationResponse) *MCPElicitationResponse {
	if response == nil {
		return &MCPElicitationResponse{
			Action: MCPElicitationActionCancel,
			Meta:   UserVerificationReasonMeta(UserVerificationReasonInvalidResponse),
		}
	}
	reason, _ := UserVerificationReasonFromMeta(response.Meta)
	out := &MCPElicitationResponse{
		Action:  response.Action,
		Content: cloneJSONValue(response.Content),
	}
	switch out.Action {
	case MCPElicitationActionAccept:
		if !validUserVerificationProof(out.Content) {
			out.Action = MCPElicitationActionCancel
			out.Content = nil
			out.Meta = UserVerificationReasonMeta(UserVerificationReasonInvalidProof)
			return out
		}
		return out
	case MCPElicitationActionDecline, MCPElicitationActionCancel:
		out.Content = nil
		if reasonMeta := UserVerificationReasonMeta(reason); reasonMeta != nil {
			out.Meta = reasonMeta
		} else {
			out.Meta = nil
		}
		return out
	default:
		return &MCPElicitationResponse{
			Action: MCPElicitationActionCancel,
			Meta:   UserVerificationReasonMeta(UserVerificationReasonInvalidResponse),
		}
	}
}

// IsUserVerificationElicitation reports whether a parsed MCP request uses the
// user-verification extension.
func IsUserVerificationElicitation(request *MCPElicitationRequest) bool {
	if request == nil {
		return false
	}
	return strings.TrimSpace(request.Method) == UserVerificationMode ||
		strings.TrimSpace(request.Mode) == UserVerificationMode
}

// validateMCPUserVerificationRequest mirrors Rust's parse_request: the payload
// carries only mode/title/description/challenge plus optional _meta, and the
// challenge is a bounded base64url value without padding.
func validateMCPUserVerificationRequest(params json.RawMessage) *stdioRPCError {
	invalid := func() *stdioRPCError {
		return mcpInvalidParams("invalid user-verification request")
	}
	var raw map[string]json.RawMessage
	if len(params) == 0 || json.Unmarshal(params, &raw) != nil || raw == nil {
		return invalid()
	}
	for key := range raw {
		switch key {
		case "mode", "title", "description", "challenge", "_meta":
		default:
			return invalid()
		}
	}
	var fields struct {
		Mode        *string         `json:"mode"`
		Title       *string         `json:"title"`
		Description *string         `json:"description"`
		Challenge   *string         `json:"challenge"`
		Meta        json.RawMessage `json:"_meta"`
	}
	if err := json.Unmarshal(params, &fields); err != nil {
		return invalid()
	}
	if fields.Mode == nil || *fields.Mode != UserVerificationMode ||
		fields.Title == nil || *fields.Title == "" || len(*fields.Title) > maxUserVerificationTitleBytes ||
		fields.Description == nil || len(*fields.Description) > maxUserVerificationDescriptionBytes ||
		fields.Challenge == nil || !validBase64URLBytes(*fields.Challenge, maxUserVerificationChallengeBytes) {
		return invalid()
	}
	if meta := bytes.TrimSpace(fields.Meta); len(meta) > 0 && !bytes.Equal(meta, []byte("null")) && !isMCPJSONObject(meta) {
		return invalid()
	}
	return nil
}

// validBase64URLBytes accepts only a bounded, non-empty base64url value that
// Rust's URL_SAFE_NO_PAD decoder accepts.
func validBase64URLBytes(encoded string, maxDecoded int) bool {
	if encoded == "" || len(encoded) > (maxDecoded+2)/3*4 {
		return false
	}
	decoded, err := base64.RawURLEncoding.DecodeString(encoded)
	return err == nil && len(decoded) > 0 && len(decoded) <= maxDecoded
}

func mcpUserVerificationCancel(reason UserVerificationReason) *MCPElicitationResponse {
	return &MCPElicitationResponse{
		Action: MCPElicitationActionCancel,
		Meta:   UserVerificationReasonMeta(reason),
	}
}

func validUserVerificationProof(content any) bool {
	proof, ok := content.(map[string]any)
	if !ok || len(proof) != 2 {
		return false
	}
	credentialID, ok := proof["credentialId"].(string)
	if !ok || credentialID == "" || len(credentialID) > 1024 {
		return false
	}
	signature, ok := proof["signature"].(string)
	if !ok || signature == "" || len(signature) > 172 {
		return false
	}
	decoded, err := base64.RawURLEncoding.DecodeString(signature)
	return err == nil && len(decoded) > 0 && len(decoded) <= 128
}

func (r UserVerificationReason) valid() bool {
	switch r {
	case UserVerificationReasonUserCancelled,
		UserVerificationReasonUserDeclined,
		UserVerificationReasonCredentialMissing,
		UserVerificationReasonBiometricsUnavailable,
		UserVerificationReasonProviderUnavailable,
		UserVerificationReasonAuthenticationFailed,
		UserVerificationReasonTimeout,
		UserVerificationReasonProviderError,
		UserVerificationReasonServiceError,
		UserVerificationReasonInvalidParams,
		UserVerificationReasonInvalidProof,
		UserVerificationReasonInvalidResponse,
		UserVerificationReasonResponseError,
		UserVerificationReasonResponseChannelClosed,
		UserVerificationReasonInterrupted,
		UserVerificationReasonApprovalUnavailable:
		return true
	default:
		return false
	}
}
