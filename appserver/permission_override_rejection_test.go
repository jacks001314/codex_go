package appserver

import (
	"os"
	"path/filepath"
	"testing"

	"codex_go/config"
	"codex_go/turn"
)

const permissionOverrideRequirements = `
default_permissions = "managed"

[allowed_permission_profiles]
managed = true

[permissions.managed]
extends = ":workspace"
`

// Mirrors Rust turn_processor build_thread_settings_overrides: an explicit
// permission-profile override that managed requirements disallow is rejected
// with the requirement warning, while the startup config path may still fall
// back.
func TestTurnStartRejectsDisallowedPermissionOverrideLikeRust(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, "requirements.toml"), []byte(permissionOverrideRequirements), 0o600); err != nil {
		t.Fatal(err)
	}
	router := NewRuntimeRouter(RuntimeServices{Config: config.NewConfigService(home), DefaultCWD: home})

	disallowed := ":danger-full-access"
	response := router.Handle(requestWithParams(t, IntID(1), MethodTurnStart, turn.TurnStartParams{
		ThreadID:    "thread-1",
		Prompt:      "hello",
		Permissions: &disallowed,
	}))
	want := "invalid thread settings override: Configured value for `permission_profile` is disallowed by requirements; falling back from `:danger-full-access` to required value `managed`."
	if response.Error == nil || response.Error.Message != want {
		t.Fatalf("turn/start error = %+v, want %q", response.Error, want)
	}

	// The allowed profile is not rejected by this check (the rest of the turn
	// flow may still fail for unrelated missing services).
	allowed := "managed"
	allowedResponse := router.Handle(requestWithParams(t, IntID(2), MethodTurnStart, turn.TurnStartParams{
		ThreadID:    "thread-1",
		Prompt:      "hello",
		Permissions: &allowed,
	}))
	if allowedResponse.Error != nil && allowedResponse.Error.Message == want {
		t.Fatalf("allowed profile was rejected: %+v", allowedResponse.Error)
	}
}

// Mirrors Rust turn_processor for thread settings updates: the same explicit
// override is rejected with the settings-override prefix.
func TestThreadSettingsUpdateRejectsDisallowedPermissionOverrideLikeRust(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, "requirements.toml"), []byte(permissionOverrideRequirements), 0o600); err != nil {
		t.Fatal(err)
	}
	router := NewRuntimeRouter(RuntimeServices{
		Config:       config.NewConfigService(home),
		DefaultCWD:   home,
		ThreadExtras: NewThreadExtraService(),
	})
	router.requireThreadStatus().UpsertThread("thread-1", false)
	disallowed := ":danger-full-access"
	response := router.Handle(requestWithParams(t, IntID(3), MethodThreadSettingsUpdate, SettingsUpdateParams{
		ThreadID:    "thread-1",
		Permissions: &disallowed,
	}))
	want := "invalid thread settings override: Configured value for `permission_profile` is disallowed by requirements; falling back from `:danger-full-access` to required value `managed`."
	if response.Error == nil || response.Error.Message != want {
		t.Fatalf("thread/settings/update error = %+v, want %q", response.Error, want)
	}
}
