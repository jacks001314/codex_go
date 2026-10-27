package appserver

import (
	"testing"

	"codex_go/auth"
)

func reloadTestSnapshot(user string, workspace string, login string, token string) *auth.AuthDotJSON {
	tokens := map[string]any{
		"access_token":    token,
		"chatgpt_user_id": user,
		"account_id":      workspace,
	}
	if login != "" {
		tokens["login_owner_id"] = login
	}
	return &auth.AuthDotJSON{AuthMode: "chatgpt", Tokens: tokens}
}

// TestRuntimeRouterRejectsStaleReloadResultsLikeRust covers Rust #55646: a
// reload that started before a login change must not overwrite the newer
// credential.
func TestRuntimeRouterRejectsStaleReloadResultsLikeRust(t *testing.T) {
	router := NewRuntimeRouter(RuntimeServices{})
	router.requireAccount().ApplyAuthSnapshot(reloadTestSnapshot("user-a", "workspace-a", "login-1", "token-1"))
	router.noteAuthChanged()
	generation := router.authChangeGeneration()

	router.requireAccount().ApplyAuthSnapshot(reloadTestSnapshot("user-b", "workspace-b", "login-2", "token-2"))
	router.noteAuthChanged()
	if router.publishReloadedAuth(generation, reloadTestSnapshot("user-a", "workspace-a", "login-1", "stale")) {
		t.Fatal("stale reload result was published")
	}
	got := router.requireAccount().AuthSnapshot()
	if got == nil || got.Tokens["chatgpt_user_id"] != "user-b" || got.Tokens["access_token"] != "token-2" {
		t.Fatalf("cached credential = %#v, want the concurrent login", got)
	}
}

// TestRuntimeRouterLogoutInvalidatesPendingReadsWhenCacheEmptyLikeRust covers
// Rust #55646: logout advances the generation even when there was nothing
// cached, so an in-flight reload cannot restore credentials.
func TestRuntimeRouterLogoutInvalidatesPendingReadsWhenCacheEmptyLikeRust(t *testing.T) {
	router := NewRuntimeRouter(RuntimeServices{})
	if snapshot := router.requireAccount().AuthSnapshot(); snapshot != nil {
		t.Fatalf("initial snapshot = %#v, want none", snapshot)
	}
	generation := router.authChangeGeneration()
	router.invalidatePendingAuthReads()
	if router.authChangeGeneration() == generation {
		t.Fatal("logout did not invalidate pending reads")
	}
	if router.publishReloadedAuth(generation, reloadTestSnapshot("user-a", "workspace-a", "login-1", "token-1")) {
		t.Fatal("reload that started before logout was published")
	}
	if snapshot := router.requireAccount().AuthSnapshot(); snapshot != nil {
		t.Fatalf("logout restored credentials: %#v", snapshot)
	}
}

// TestRuntimeRouterPublishesReloadAndAdvancesLoginGenerationLikeRust verifies
// that a fresh reload is published and that losing login metadata advances only
// the login revision.
func TestRuntimeRouterPublishesReloadAndAdvancesLoginGenerationLikeRust(t *testing.T) {
	router := NewRuntimeRouter(RuntimeServices{})
	router.requireAccount().ApplyAuthSnapshot(reloadTestSnapshot("user-a", "workspace-a", "login-1", "token-1"))
	router.noteAuthChanged()
	before := router.authChangeTracker.Snapshot()
	generation := router.authChangeGeneration()

	lost := reloadTestSnapshot("user-a", "workspace-a", "", "token-2")
	if !router.publishReloadedAuth(generation, lost) {
		t.Fatal("fresh reload was rejected")
	}
	after := router.authChangeTracker.Snapshot()
	if after.LoginGeneration != before.LoginGeneration+1 {
		t.Fatalf("login generation = %d, want %d", after.LoginGeneration, before.LoginGeneration+1)
	}
	if after.OwnerGeneration != before.OwnerGeneration {
		t.Fatalf("owner generation = %d, want unchanged %d", after.OwnerGeneration, before.OwnerGeneration)
	}
	if got := router.requireAccount().AuthSnapshot(); got == nil || got.Tokens["access_token"] != "token-2" {
		t.Fatalf("published credential = %#v", got)
	}
}
