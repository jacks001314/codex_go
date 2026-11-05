package runtimeauth

import (
	"context"
	"testing"

	"codex_go/auth"
)

func TestSourceGenerationIgnoresTokenRefreshLikeRust(t *testing.T) {
	before := chatgptSnapshot("token-old")
	before.Tokens["login_owner_id"] = "login-1"
	after := chatgptSnapshot("token-new")
	after.Tokens["login_owner_id"] = "login-1"
	if SourceGeneration(before) != SourceGeneration(after) {
		t.Fatal("a same-login token refresh must keep the credential source")
	}
	otherLogin := chatgptSnapshot("token-old")
	otherLogin.Tokens["login_owner_id"] = "login-2"
	if SourceGeneration(before) == SourceGeneration(otherLogin) {
		t.Fatal("a new login must change the credential source")
	}
	if SourceGeneration(apiKeySnapshot("sk-1")) == SourceGeneration(apiKeySnapshot("sk-2")) {
		t.Fatal("a rotated API key must change the credential source")
	}
}

func TestManagerDerivesSourceGenerationWithoutCallback(t *testing.T) {
	fake := newFakeRegistrationServer(t)
	snapshot := chatgptSnapshot("token-1")
	snapshot.Tokens["login_owner_id"] = "login-1"
	manager, err := NewManager(ManagerOptions{
		Credentials:     func(context.Context) (*auth.AuthDotJSON, error) { return snapshot, nil },
		InstallationID:  "99999999-9999-4999-8999-999999999999",
		RegistrationURL: fake.server.URL,
		HTTPClient:      fake.server.Client(),
	})
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}
	if _, err := manager.Auth(context.Background()); err != nil {
		t.Fatalf("Auth() error = %v", err)
	}
	refreshed := chatgptSnapshot("token-2")
	refreshed.Tokens["login_owner_id"] = "login-1"
	snapshot = refreshed
	if _, err := manager.Auth(context.Background()); err != nil {
		t.Fatalf("Auth() after refresh error = %v", err)
	}
	if fake.count() != 1 {
		t.Fatalf("registrations = %d, want the refreshed token to reuse the runtime", fake.count())
	}
	if fake.count() != 1 {
		t.Fatalf("registrations = %d, want 1", fake.count())
	}
}

func TestModeFromConfigValuesLikeRust(t *testing.T) {
	if got := ParseMode(""); got != ModeDualHeader {
		t.Fatalf("default mode = %q", got)
	}
	if got := ModeFromConfigValues(map[string]any{"runtime_agent_instances": map[string]any{"mode": "assertion_only"}}); got != ModeAssertionOnly {
		t.Fatalf("configured mode = %q", got)
	}
	if got := ModeFromConfigValues(map[string]any{"runtime_agent_instances": map[string]any{"mode": "dual_header"}}); got != ModeDualHeader {
		t.Fatalf("configured dual mode = %q", got)
	}
	if got := ModeFromConfigValues(nil); got != ModeDualHeader {
		t.Fatalf("absent config mode = %q", got)
	}
}
