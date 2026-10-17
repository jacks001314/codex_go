package model

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"codex_go/auth"
)

// Rust #53991: the managed-auth recovery step that refreshes after a 401 is
// RefreshReason::UnauthorizedRecovery, not the `forced` default. The runner's
// refreshManagedChatGPTAuth is that step (model/auth_recovery.go
// UnauthorizedRecoveryStepRefreshToken).
func TestManagedAuthRecoveryReportsUnauthorizedRefreshReasonLikeRust(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("CODEX_API_KEY", "")
	t.Setenv("CODEX_ACCESS_TOKEN", "")
	home := t.TempDir()
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
	loaded, err := auth.NewStore(home).Load()
	if err != nil || loaded == nil {
		t.Fatalf("Load() = %#v, %v", loaded, err)
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

	runner := &ResponsesAgentRunner{CodexHome: home, AuthSnapshot: loaded, AuthIssuer: server.URL}
	if err := runner.refreshManagedChatGPTAuth(context.Background()); err != nil {
		t.Fatalf("refreshManagedChatGPTAuth() error = %v", err)
	}
	if got := requestBody["refresh_reason"]; got != "unauthorized_recovery" {
		t.Fatalf("refresh_reason = %q, want unauthorized_recovery (body %#v)", got, requestBody)
	}
	if runner.AuthSnapshot == nil || runner.AuthSnapshot.Tokens["access_token"] != "new-access-token" {
		t.Fatalf("runner auth was not refreshed: %#v", runner.AuthSnapshot)
	}
}
