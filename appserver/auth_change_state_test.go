package appserver

import (
	"testing"

	"codex_go/auth"
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
	router.requireAccount().ApplyAuthSnapshot(snapshot("user-b", "workspace-a", "token-3"))
	router.noteAuthChanged()
	if got := router.authOwnerRevisionSnapshot(); got != 2 {
		t.Fatalf("account switch owner revision = %d, want 2", got)
	}

	// Logout clears the owner.
	router.requireAccount().ApplyAuthSnapshot(nil)
	router.noteAuthChanged()
	if got := router.authOwnerRevisionSnapshot(); got != 3 {
		t.Fatalf("logout owner revision = %d, want 3", got)
	}
}
