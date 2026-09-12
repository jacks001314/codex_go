package appserver

import (
	"testing"

	"codex_go/auth"
	"codex_go/model"
)

// TestRuntimeRouterAuthOwnerRevisionTracksIdentityChanges covers the app-server
// wiring of Rust's AuthChangeState (#43428): the ownership revision advances on
// login, logout, and identity changes but not on a same-owner token refresh.
func TestRuntimeRouterAuthOwnerRevisionTracksIdentityChanges(t *testing.T) {
	router := NewRuntimeRouter(RuntimeServices{})
	snapshot := func(user string, workspace string, token string) *auth.AuthDotJSON {
		return &auth.AuthDotJSON{
			AuthMode: "chatgpt",
			Tokens: map[string]any{
				"access_token":    token,
				"chatgpt_user_id": user,
				"account_id":      workspace,
			},
		}
	}

	router.requireAccount().ApplyAuthSnapshot(snapshot("user-a", "workspace-a", "token-1"))
	router.noteAuthChanged()
	if got := router.authOwnerRevisionSnapshot(); got != 1 {
		t.Fatalf("first login owner revision = %d, want 1", got)
	}

	// Same owner, new access token: ownership is unchanged.
	router.requireAccount().ApplyAuthSnapshot(snapshot("user-a", "workspace-a", "token-2"))
	router.noteAuthChanged()
	if got := router.authOwnerRevisionSnapshot(); got != 1 {
		t.Fatalf("token refresh advanced owner revision to %d, want 1", got)
	}

	// Account switch changes ownership.
	router.services.Agent = &model.ResponsesAgentRunner{}
	router.requireAccount().ApplyAuthSnapshot(snapshot("user-b", "workspace-a", "token-3"))
	router.noteAuthChanged()
	if got := router.authOwnerRevisionSnapshot(); got != 2 {
		t.Fatalf("account switch owner revision = %d, want 2", got)
	}
	if router.services.Agent != nil {
		t.Fatal("cached responses agent survived an auth ownership change")
	}

	// Logout clears the owner.
	router.services.Agent = &model.ResponsesAgentRunner{}
	router.requireAccount().ApplyAuthSnapshot(nil)
	router.noteAuthChanged()
	if got := router.authOwnerRevisionSnapshot(); got != 3 {
		t.Fatalf("logout owner revision = %d, want 3", got)
	}
	if router.services.Agent != nil {
		t.Fatal("cached responses agent survived logout")
	}

	// A same-owner token refresh must not drop the cached agent.
	router.requireAccount().ApplyAuthSnapshot(snapshot("user-c", "workspace-c", "token-4"))
	router.noteAuthChanged()
	router.services.Agent = &model.ResponsesAgentRunner{}
	router.requireAccount().ApplyAuthSnapshot(snapshot("user-c", "workspace-c", "token-5"))
	router.noteAuthChanged()
	if router.services.Agent == nil {
		t.Fatal("same-owner token refresh dropped the cached responses agent")
	}

	// Injected/custom runners are not owned by the router and must survive.
	custom := &model.UnavailableAgentRunner{}
	router.services.Agent = custom
	router.requireAccount().ApplyAuthSnapshot(snapshot("user-d", "workspace-d", "token-6"))
	router.noteAuthChanged()
	if router.services.Agent != custom {
		t.Fatal("injected agent runner was discarded on an auth ownership change")
	}
}
