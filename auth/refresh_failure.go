package auth

import (
	"encoding/json"
	"errors"
	"strings"
	"sync"
)

type RefreshTokenFailedReason string

const (
	RefreshTokenFailedExpired   RefreshTokenFailedReason = "expired"
	RefreshTokenFailedExhausted RefreshTokenFailedReason = "exhausted"
	RefreshTokenFailedRevoked   RefreshTokenFailedReason = "revoked"
	RefreshTokenFailedOther     RefreshTokenFailedReason = "other"
)

const (
	refreshTokenExpiredMessage = "Your access token could not be refreshed because your refresh token has expired. Please log out and sign in again."
	refreshTokenReusedMessage  = "Your access token could not be refreshed because your refresh token was already used. Please log out and sign in again."
	refreshTokenRevokedMessage = "Your access token could not be refreshed because your refresh token was revoked. Please log out and sign in again."
	refreshTokenUnknownMessage = "Your access token could not be refreshed. Please log out and sign in again."
)

type RefreshTokenFailedError struct {
	Reason  RefreshTokenFailedReason `json:"reason"`
	Message string                   `json:"message"`
}

func (e *RefreshTokenFailedError) Error() string {
	if e == nil {
		return refreshTokenUnknownMessage
	}
	if strings.TrimSpace(e.Message) != "" {
		return e.Message
	}
	return refreshTokenUnknownMessage
}

func (e *RefreshTokenFailedError) Clone() *RefreshTokenFailedError {
	if e == nil {
		return nil
	}
	return &RefreshTokenFailedError{Reason: e.Reason, Message: e.Message}
}

func IsPermanentRefreshFailure(err error) bool {
	var failed *RefreshTokenFailedError
	return errors.As(err, &failed)
}

func RefreshTokenFailedReasonFromError(err error) *RefreshTokenFailedReason {
	var failed *RefreshTokenFailedError
	if !errors.As(err, &failed) || failed == nil {
		return nil
	}
	reason := failed.Reason
	return &reason
}

type scopedRefreshFailure struct {
	Auth  *AuthDotJSON
	Error *RefreshTokenFailedError
}

var refreshFailureState = struct {
	sync.Mutex
	byHome map[string]*scopedRefreshFailure
}{byHome: map[string]*scopedRefreshFailure{}}

func RecordPermanentRefreshFailureIfUnchanged(codexHome string, attempted *AuthDotJSON, failed *RefreshTokenFailedError) {
	RecordPermanentRefreshFailureIfUnchangedWithOptions(codexHome, attempted, failed, nil)
}

func RecordPermanentRefreshFailureIfUnchangedWithOptions(codexHome string, attempted *AuthDotJSON, failed *RefreshTokenFailedError, storeOptions *StoreOptions) {
	if attempted == nil || failed == nil {
		return
	}
	codexHome = strings.TrimSpace(codexHome)
	if codexHome != "" {
		current, err := NewStoreWithOptions(codexHome, storeOptions).Resolve()
		if err == nil && current != nil && !AuthsEqualForRefresh(attempted, &current.Auth) {
			return
		}
	}
	refreshFailureState.Lock()
	defer refreshFailureState.Unlock()
	refreshFailureState.byHome[refreshFailureScopeKey(codexHome)] = &scopedRefreshFailure{
		Auth:  cloneAuthDotJSON(attempted),
		Error: failed.Clone(),
	}
}

func RefreshFailureForAuth(codexHome string, snapshot *AuthDotJSON) *RefreshTokenFailedError {
	if snapshot == nil {
		return nil
	}
	refreshFailureState.Lock()
	defer refreshFailureState.Unlock()
	failure := refreshFailureState.byHome[refreshFailureScopeKey(codexHome)]
	if failure == nil || !AuthsEqualForRefresh(snapshot, failure.Auth) {
		return nil
	}
	return failure.Error.Clone()
}

func ClearPermanentRefreshFailure(codexHome string) {
	refreshFailureState.Lock()
	defer refreshFailureState.Unlock()
	delete(refreshFailureState.byHome, refreshFailureScopeKey(codexHome))
}

func AuthsEqualForRefresh(left *AuthDotJSON, right *AuthDotJSON) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	if left.Mode() != right.Mode() {
		return false
	}
	switch left.Mode() {
	case "api-key":
		return strings.TrimSpace(left.OpenAIAPIKey) == strings.TrimSpace(right.OpenAIAPIKey)
	case "chatgpt":
		return authFingerprint(left) == authFingerprint(right)
	case "chatgptAuthTokens":
		// Rust b28aa476f4 (#38054): compare external ChatGPT token data
		// instead of the full auth JSON so a refreshed access token (with
		// updated metadata such as plan type or email) is recognized as the
		// same auth and applied to a retried request.
		return authTokenDataEqual(left.Tokens, right.Tokens)
	case "personal-access-token":
		return strings.TrimSpace(left.PersonalAccessToken) == strings.TrimSpace(right.PersonalAccessToken)
	case "agent-identity", "bedrock-api-key":
		return authFingerprint(left) == authFingerprint(right)
	default:
		return authFingerprint(left) == authFingerprint(right)
	}
}

// authTokenDataEqual compares only the token data of external ChatGPT auth
// (Rust CodexAuth::get_current_token_data equivalence). JSON marshaling is
// deterministic because encoding/json sorts map keys.
func authTokenDataEqual(left map[string]any, right map[string]any) bool {
	return authTokenDataFingerprint(left) == authTokenDataFingerprint(right)
}

func authTokenDataFingerprint(tokens map[string]any) string {
	if len(tokens) == 0 {
		return ""
	}
	data, err := json.Marshal(tokens)
	if err != nil {
		return ""
	}
	return string(data)
}

func refreshFailureScopeKey(codexHome string) string {
	return strings.TrimSpace(codexHome)
}

func cloneAuthDotJSON(snapshot *AuthDotJSON) *AuthDotJSON {
	if snapshot == nil {
		return nil
	}
	clone := *snapshot
	clone.Tokens = cloneAnyMap(snapshot.Tokens)
	clone.AgentIdentity = cloneAgentIdentityStorage(snapshot.AgentIdentity)
	return &clone
}

func cloneAgentIdentityStorage(value any) any {
	switch record := value.(type) {
	case *AgentIdentityAuthRecord:
		if record == nil {
			return nil
		}
		clone := *record
		clone.Email = cloneStringPtr(record.Email)
		clone.TaskID = cloneStringPtr(record.TaskID)
		return &clone
	case AgentIdentityAuthRecord:
		clone := record
		clone.Email = cloneStringPtr(record.Email)
		clone.TaskID = cloneStringPtr(record.TaskID)
		return clone
	case map[string]any:
		return cloneAnyMap(record)
	default:
		return value
	}
}

func authFingerprint(snapshot *AuthDotJSON) string {
	if snapshot == nil {
		return ""
	}
	data, err := json.Marshal(snapshot)
	if err != nil {
		return ""
	}
	return string(data)
}

func classifyRefreshTokenFailure(body string) *RefreshTokenFailedError {
	switch strings.ToLower(strings.TrimSpace(extractRefreshTokenErrorCode(body))) {
	case "refresh_token_expired":
		return &RefreshTokenFailedError{Reason: RefreshTokenFailedExpired, Message: refreshTokenExpiredMessage}
	case "refresh_token_reused":
		return &RefreshTokenFailedError{Reason: RefreshTokenFailedExhausted, Message: refreshTokenReusedMessage}
	case "refresh_token_invalidated":
		return &RefreshTokenFailedError{Reason: RefreshTokenFailedRevoked, Message: refreshTokenRevokedMessage}
	default:
		return &RefreshTokenFailedError{Reason: RefreshTokenFailedOther, Message: refreshTokenUnknownMessage}
	}
}

func extractRefreshTokenErrorCode(body string) string {
	if strings.TrimSpace(body) == "" {
		return ""
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		return ""
	}
	// RFC 6749 reports an unusable refresh token as a plain-string error code
	// ({"error":"invalid_grant"}); legacy backend responses nest it as
	// {"error":{"code":"refresh_token_*"}}. Mirror Rust's extractor, which
	// accepts both shapes plus a top-level "code" field (#39637).
	switch value := payload["error"].(type) {
	case string:
		return strings.TrimSpace(value)
	case map[string]any:
		if code, ok := value["code"].(string); ok {
			return strings.TrimSpace(code)
		}
	}
	if code, ok := payload["code"].(string); ok {
		return strings.TrimSpace(code)
	}
	return ""
}
