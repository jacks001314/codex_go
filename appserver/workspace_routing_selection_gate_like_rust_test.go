package appserver

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"codex_go/auth"
	"codex_go/config"
	"codex_go/model"
)

// workspaceRoutingRouterWithAuth mirrors workspaceRoutingRouter but lets a test
// choose the stored credential, which is what selects the workspace-routing
// branch of account/read.
func workspaceRoutingRouterWithAuth(t *testing.T, snapshot auth.AuthDotJSON, handler http.HandlerFunc) *RuntimeRouter {
	t.Helper()
	clearAuthEnvAppserver(t)
	home := t.TempDir()
	if err := auth.NewStore(home).Save(snapshot); err != nil {
		t.Fatalf("auth save error: %v", err)
	}
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	if err := os.WriteFile(config.ConfigPath(home), []byte(`chatgpt_base_url = "`+server.URL+`"`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return NewRuntimeRouter(RuntimeServices{
		Account: auth.NewAccountManager(),
		Config:  config.NewConfigService(home),
	})
}

// TestRuntimeRouterGetAccountWithoutSelectedWorkspaceReturnsNullRoutingLikeRust
// mirrors Rust #45529 `saved_chatgpt_login_without_selected_workspace_preserves_account`:
// a ChatGPT credential with no account id reports `workspaceRouting: null` and
// never contacts accounts/check.
func TestRuntimeRouterGetAccountWithoutSelectedWorkspaceReturnsNullRoutingLikeRust(t *testing.T) {
	requests := 0
	router := workspaceRoutingRouterWithAuth(t, auth.FromChatGPTAuthTokens("chatgpt-token", "", nil), func(w http.ResponseWriter, r *http.Request) {
		requests++
		t.Errorf("accounts/check called without a selected workspace: %s", r.URL.Path)
	})

	response := router.Handle(requestWithParams(t, IntID(1), MethodGetAccount, auth.GetAccountParams{}))
	if response.Error != nil {
		t.Fatalf("get account = %+v", response.Error)
	}
	account, ok := response.Result.(*auth.GetAccountResponse)
	if !ok || account == nil {
		t.Fatalf("response result = %+v", response.Result)
	}
	if account.WorkspaceRouting != nil {
		t.Fatalf("workspace routing = %+v, want null", account.WorkspaceRouting)
	}
	if requests != 0 {
		t.Fatalf("accounts/check requests = %d, want 0", requests)
	}
}

// TestWorkspaceRoutingRequiresPersonalAccessTokenAccountIDLikeRust covers the
// one auth mode Rust treats as always carrying an id: an empty
// personal-access-token account id is an internal error rather than null.
func TestWorkspaceRoutingRequiresPersonalAccessTokenAccountIDLikeRust(t *testing.T) {
	router := workspaceRoutingRouterWithAuth(t, auth.FromPersonalAccessToken("pat-token", nil), func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("accounts/check called for a personal access token: %s", r.URL.Path)
	})
	snapshot := auth.FromPersonalAccessToken("pat-token", nil)
	routing, err := router.workspaceRoutingForAccountRead(context.Background(), &snapshot)
	if routing != nil {
		t.Fatalf("workspace routing = %+v, want nil", routing)
	}
	if !errors.Is(err, model.ErrWorkspaceRoutingMissingAccountID) {
		t.Fatalf("error = %v, want %v", err, model.ErrWorkspaceRoutingMissingAccountID)
	}
}
