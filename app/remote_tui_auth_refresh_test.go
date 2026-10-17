package app

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"codex_go/auth"
)

// Rust #53991: the TUI answers the app server's ChatGPT token refresh request
// because the server hit an unauthorized response, so the outbound OAuth
// request reports `unauthorized_recovery` instead of the `forced` default.
func TestRemoteTUIAuthRefreshReportsUnauthorizedReasonLikeRust(t *testing.T) {
	for _, key := range []string{"OPENAI_API_KEY", "CODEX_API_KEY", "CODEX_ACCESS_TOKEN"} {
		t.Setenv(key, "")
	}
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	header, _ := json.Marshal(map[string]string{"alg": "none"})
	claims, _ := json.Marshal(map[string]any{"chatgpt_account_id": "account-123"})
	idToken := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(claims) + ".sig"
	if err := auth.PersistChatGPTTokens(home, &auth.ExchangedTokens{
		IDToken:      idToken,
		AccessToken:  "old-access-token",
		RefreshToken: "old-refresh-token",
	}); err != nil {
		t.Fatalf("PersistChatGPTTokens() error = %v", err)
	}

	var requestBody map[string]string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&requestBody); err != nil {
			t.Errorf("Decode() error = %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"new-access-token","refresh_token":"new-refresh-token"}`))
	}))
	defer server.Close()
	t.Setenv(auth.RefreshTokenURLEnvOverride, server.URL)

	client := &remoteAppServerTUIClient{}
	response, err := client.chatGPTAuthTokensRefresh(context.Background(), &auth.ChatGPTAuthTokensRefreshParams{
		Reason: auth.RefreshUnauthorized,
	})
	if err != nil {
		t.Fatalf("chatGPTAuthTokensRefresh() error = %v", err)
	}
	if got := requestBody["refresh_reason"]; got != "unauthorized_recovery" {
		t.Fatalf("refresh_reason = %q, want unauthorized_recovery (body %#v)", got, requestBody)
	}
	if response == nil || response.AccessToken != "new-access-token" || response.ChatGPTAccountID != "account-123" {
		t.Fatalf("response = %#v", response)
	}
}
