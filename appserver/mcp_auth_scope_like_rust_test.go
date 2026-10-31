package appserver

import (
	"testing"

	"codex_go/auth"
)

// TestMCPConnectionAuthScopeSurvivesRefreshButNotOwnerChangeLikeRust mirrors
// Rust #57211's connection_key_changes_with_the_owner_but_survives_credential_refresh:
// the scope handed to the MCP connection pool keeps a credential refresh but
// changes with the account owner, and two trackers never share a scope.
func TestMCPConnectionAuthScopeSurvivesRefreshButNotOwnerChangeLikeRust(t *testing.T) {
	router := NewRuntimeRouter(RuntimeServices{})
	source := routerMCPAuthChangeSource{router: router}
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
	initial := source.MCPConnectionAuthScope()
	if initial == "" {
		t.Fatal("scope without a tracker revision is empty")
	}

	// Same owner, new access token: the pooled connection stays shareable.
	router.requireAccount().ApplyAuthSnapshot(snapshot("user-a", "workspace-a", "token-2"))
	router.noteAuthChanged()
	if got := source.MCPConnectionAuthScope(); got != initial {
		t.Fatalf("credential refresh changed the connection scope: %q -> %q", initial, got)
	}

	// Account switch changes the credential source.
	router.requireAccount().ApplyAuthSnapshot(snapshot("user-b", "workspace-a", "token-3"))
	router.noteAuthChanged()
	if got := source.MCPConnectionAuthScope(); got == initial {
		t.Fatal("account switch kept the connection scope")
	}

	// A different router's tracker is a different credential source even when
	// its revisions match.
	other := routerMCPAuthChangeSource{router: NewRuntimeRouter(RuntimeServices{})}
	if got := other.MCPConnectionAuthScope(); got == "" || got == initial {
		t.Fatalf("second tracker scope = %q, want a distinct non-empty scope", got)
	}

	if got := (routerMCPAuthChangeSource{}).MCPConnectionAuthScope(); got != "" {
		t.Fatalf("scope without a router = %q, want empty", got)
	}
}
