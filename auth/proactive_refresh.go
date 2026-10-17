package auth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"
	"time"
)

// Rust `login/src/auth/manager.rs` refresh cadence constants (#53991).
const (
	// ChatGPTAccessTokenRefreshWindowMinutes mirrors
	// `CHATGPT_ACCESS_TOKEN_REFRESH_WINDOW_MINUTES`.
	ChatGPTAccessTokenRefreshWindowMinutes = 5
	// ChatGPTTokenRefreshIntervalDays mirrors `TOKEN_REFRESH_INTERVAL`.
	ChatGPTTokenRefreshIntervalDays = 8
)

// JWTAccessTokenExpiration mirrors Rust `parse_jwt_expiration`
// (`login/src/token_data.rs`): the token must have exactly three non-empty
// segments, a base64url JSON payload, and an integer `exp` claim. Anything else
// reports ok=false so callers fall back to `last_refresh`, exactly like the
// Rust `if let Ok(Some(expires_at)) = parse_jwt_expiration(..)` guard.
func JWTAccessTokenExpiration(token string) (time.Time, bool) {
	parts := strings.Split(strings.TrimSpace(token), ".")
	if len(parts) != 3 {
		return time.Time{}, false
	}
	for _, part := range parts {
		if part == "" {
			return time.Time{}, false
		}
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return time.Time{}, false
	}
	var claims struct {
		Exp *int64 `json:"exp"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil || claims.Exp == nil {
		return time.Time{}, false
	}
	return time.Unix(*claims.Exp, 0).UTC(), true
}

// ProactiveRefreshReason mirrors Rust `AuthManager::proactive_refresh_reason`
// (#53991). Only managed ChatGPT auth is eligible: an access token expiring
// within the five-minute refresh window reports `proactive_expiry`; when the
// access token has no readable `exp`, an auth last refreshed more than eight
// days ago reports `scheduled`. A readable `exp` beyond the window never falls
// back to the scheduled check, matching Rust.
func ProactiveRefreshReason(snapshot *AuthDotJSON, now time.Time) (RefreshReason, bool) {
	if snapshot == nil || snapshot.Mode() != "chatgpt" {
		return "", false
	}
	if snapshot.Tokens != nil {
		if expiresAt, ok := JWTAccessTokenExpiration(stringFromAny(snapshot.Tokens, "access_token")); ok {
			if !expiresAt.After(now.Add(ChatGPTAccessTokenRefreshWindowMinutes * time.Minute)) {
				return RefreshReasonProactiveExpiry, true
			}
			return "", false
		}
	}
	lastRefresh, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(snapshot.LastRefresh))
	if err != nil {
		return "", false
	}
	if lastRefresh.Before(now.Add(-ChatGPTTokenRefreshIntervalDays * 24 * time.Hour)) {
		return RefreshReasonScheduled, true
	}
	return "", false
}

// ProactiveRefreshManagedChatGPT mirrors the pre-request half of Rust
// `AuthManager::auth()` (#53991): when the cached managed ChatGPT auth is due
// for a refresh, the request reports the matching `refresh_reason`. A failed
// refresh is non-fatal, exactly as in Rust, which logs the error and keeps
// serving the cached auth; the original snapshot is returned then.
func ProactiveRefreshManagedChatGPT(ctx context.Context, opts *RefreshChatGPTTokenOptions, now time.Time) (*AuthDotJSON, RefreshReason, bool, error) {
	if opts == nil || opts.AuthSnapshot == nil {
		return nil, "", false, nil
	}
	reason, ok := ProactiveRefreshReason(opts.AuthSnapshot, now)
	if !ok {
		return nil, "", false, nil
	}
	request := *opts
	request.Reason = reason
	refreshed, err := RefreshChatGPTTokens(ctx, &request)
	if err != nil {
		return opts.AuthSnapshot, reason, true, err
	}
	if refreshed == nil {
		return opts.AuthSnapshot, reason, true, nil
	}
	return refreshed, reason, true, nil
}
