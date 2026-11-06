package appserver

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"codex_go/auth"
	"codex_go/config"
)

// TestRuntimeRouterLogoutRequiresSuccessfulAuthReloadLikeRust covers Rust
// #59823: when the persisted credential cannot be read, logout fails with an
// internal error, leaves the cached identity and the store untouched, and
// revokes nothing. A later logout against a readable store succeeds.
func TestRuntimeRouterLogoutRequiresSuccessfulAuthReloadLikeRust(t *testing.T) {
	home := t.TempDir()
	authFile := filepath.Join(home, "auth.json")
	if err := os.WriteFile(authFile, []byte("{"), 0o600); err != nil {
		t.Fatalf("write unreadable auth: %v", err)
	}
	revoked := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		revoked++
		writeJSON(t, w, map[string]string{"message": "success"})
	}))
	defer server.Close()
	t.Setenv(auth.RevokeTokenURLEnvOverride, server.URL+"/oauth/revoke")

	router := NewRuntimeRouter(RuntimeServices{
		Account: auth.NewAccountManager(),
		Config:  config.NewConfigService(home),
	})
	failed := router.Handle(requestWithParams(t, IntID(1), MethodLogoutAccount, map[string]any{}))
	if failed.Error == nil || failed.Error.Code != JSONRPCInternalErrorCode {
		t.Fatalf("unreadable store logout = %+v, want internal error", failed)
	}
	if revoked != 0 {
		t.Fatalf("unreadable store revoked %d token(s)", revoked)
	}
	if got, err := os.ReadFile(authFile); err != nil || string(got) != "{" {
		t.Fatalf("unreadable store after logout = %q, %v", got, err)
	}

	if err := auth.PersistChatGPTTokens(home, &auth.ExchangedTokens{
		IDToken:      fakeJWTAppserver(map[string]any{"chatgpt_account_id": "account-1"}),
		AccessToken:  "access-token",
		RefreshToken: "refresh-token",
	}); err != nil {
		t.Fatalf("save readable auth: %v", err)
	}
	ok := router.Handle(requestWithParams(t, IntID(2), MethodLogoutAccount, map[string]any{}))
	if ok.Error != nil {
		t.Fatalf("readable store logout = %+v", ok)
	}
	if revoked != 1 {
		t.Fatalf("revoke requests = %d, want 1", revoked)
	}
	if _, err := os.Stat(authFile); !os.IsNotExist(err) {
		t.Fatalf("auth.json after logout stat error = %v, want not exist", err)
	}
}
