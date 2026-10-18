package auth

import (
	"encoding/base64"
	"encoding/json"
	"testing"
)

func accountUserIDJWT(claims map[string]any) string {
	payload, _ := json.Marshal(claims)
	return "eyJhbGciOiJub25lIn0." + base64.RawURLEncoding.EncodeToString(payload) + "."
}

// Mirrors Rust #43524: the opaque account-user claim is accepted only when its
// workspace matches the selected account.
func TestChatGPTAccountUserIDRequiresMatchingWorkspaceLikeRust(t *testing.T) {
	for _, tc := range []struct {
		name   string
		tokens map[string]any
		want   string
	}{
		{
			name: "matching workspace",
			tokens: map[string]any{
				"account_id":   "workspace-a",
				"access_token": accountUserIDJWT(map[string]any{"chatgpt_account_id": "workspace-a", "chatgpt_account_user_id": "account-user-a", "chatgpt_user_id": "user-a"}),
			},
			want: "account-user-a",
		},
		{
			name: "other workspace",
			tokens: map[string]any{
				"account_id":   "workspace-b",
				"access_token": accountUserIDJWT(map[string]any{"chatgpt_account_id": "workspace-a", "chatgpt_account_user_id": "account-user-a"}),
			},
		},
		{
			name: "missing account user claim",
			tokens: map[string]any{
				"account_id":   "workspace-a",
				"access_token": accountUserIDJWT(map[string]any{"chatgpt_account_id": "workspace-a", "chatgpt_user_id": "user-a"}),
			},
		},
		{
			name: "malformed token",
			tokens: map[string]any{
				"account_id":   "workspace-a",
				"access_token": "not-a-jwt",
			},
		},
		{
			name: "missing selected account",
			tokens: map[string]any{
				"access_token": accountUserIDJWT(map[string]any{"chatgpt_account_id": "workspace-a", "chatgpt_account_user_id": "account-user-a"}),
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := ChatGPTAccountUserIDFromAuth(&AuthDotJSON{AuthMode: "chatgpt", Tokens: tc.tokens})
			if got != tc.want {
				t.Fatalf("ChatGPTAccountUserIDFromAuth() = %q, want %q", got, tc.want)
			}
		})
	}
}
