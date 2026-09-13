package appserver

import (
	"context"
	"testing"

	"codex_go/auth"
	"codex_go/model"
	"codex_go/remotecontrol"
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

// TestRuntimeRouterRemoteControlWatchIsLoginScoped covers Rust #44341's relay
// scoping: the remote-control loop watches the login lifetime, so a same-owner
// token refresh preserves the live connection while an identity change wakes it.
func TestRuntimeRouterRemoteControlWatchIsLoginScoped(t *testing.T) {
	router := NewRuntimeRouter(RuntimeServices{})
	snapshot := func(user string, token string) *auth.AuthDotJSON {
		return &auth.AuthDotJSON{
			AuthMode: "chatgpt",
			Tokens: map[string]any{
				"access_token":    token,
				"chatgpt_user_id": user,
				"account_id":      "workspace-" + user,
			},
		}
	}
	router.requireAccount().ApplyAuthSnapshot(snapshot("owner-e", "token-1"))
	router.noteAuthChanged()
	revision, err := router.remoteControlAuthRevision(context.Background())
	if err != nil {
		t.Fatalf("remoteControlAuthRevision error = %v", err)
	}

	// A same-owner token refresh must not wake the relay.
	router.requireAccount().ApplyAuthSnapshot(snapshot("owner-e", "token-2"))
	router.noteAuthChanged()
	refreshed, err := router.remoteControlAuthRevision(context.Background())
	if err != nil {
		t.Fatalf("remoteControlAuthRevision error = %v", err)
	}
	if refreshed != revision {
		t.Fatalf("same-owner refresh advanced the remote-control revision: %d -> %d", revision, refreshed)
	}

	// An account switch must wake the relay so it reconnects as the new owner.
	router.requireAccount().ApplyAuthSnapshot(snapshot("owner-f", "token-3"))
	router.noteAuthChanged()
	switched, err := router.remoteControlAuthRevision(context.Background())
	if err != nil {
		t.Fatalf("remoteControlAuthRevision error = %v", err)
	}
	if switched == revision {
		t.Fatal("account switch did not advance the remote-control revision")
	}
}

// TestRemoteControlOnlyLoopOptionsInstallsLoginScopedRevision covers the
// remote-control-only wiring: the loop picks up the login-scoped revision while
// an explicit caller-supplied revision is preserved.
func TestRemoteControlOnlyLoopOptionsInstallsLoginScopedRevision(t *testing.T) {
	called := false
	revision := func(context.Context) (uint64, error) { called = true; return 7, nil }

	installed := remoteControlOnlyLoopOptions(nil, revision)
	if installed == nil || installed.AuthRevision == nil {
		t.Fatal("nil options did not receive the revision")
	}
	if got, _ := installed.AuthRevision(context.Background()); got != 7 || !called {
		t.Fatalf("installed revision = %d, called = %v", got, called)
	}

	existing := remoteControlOnlyLoopOptions(&remotecontrol.RemoteControlWebsocketLoopOptions{}, revision)
	if existing == nil || existing.AuthRevision == nil {
		t.Fatal("existing options did not receive the revision")
	}

	custom := func(context.Context) (uint64, error) { return 11, nil }
	preserved := remoteControlOnlyLoopOptions(&remotecontrol.RemoteControlWebsocketLoopOptions{AuthRevision: custom}, revision)
	if preserved == nil || preserved.AuthRevision == nil {
		t.Fatal("explicit revision was dropped")
	}
	if got, _ := preserved.AuthRevision(context.Background()); got != 11 {
		t.Fatalf("explicit revision = %d, want 11", got)
	}
}
