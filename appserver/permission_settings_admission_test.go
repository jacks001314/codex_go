package appserver

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"codex_go/config"
	"codex_go/sandbox"
	"codex_go/turn"
)

func TestInvalidLegacyPermissionAdmissionLeavesSettingsUnchanged(t *testing.T) {
	router := NewRuntimeRouter(RuntimeServices{})
	defer router.Close()
	service := router.requireThreadExtras()
	named := "read-only"
	if _, err := service.UpdateSettings(&SettingsUpdateParams{ThreadID: "thread", Permissions: &named}); err != nil {
		t.Fatal(err)
	}
	invalid, proposedModel := "not-a-sandbox", "new-model"
	params := &SettingsUpdateParams{ThreadID: "thread", SandboxPolicy: &invalid, Model: &proposedModel}
	if _, err := router.commitThreadSettingsUpdate(params, false); err == nil {
		t.Fatal("invalid policy admitted")
	}
	settings := service.Settings("thread")
	if settings.ActivePermissionProfile == nil || *settings.ActivePermissionProfile != named || settings.Model != "" || settings.SandboxPolicy != "" {
		t.Fatalf("invalid request changed settings: %+v", settings)
	}
	if params.SandboxPolicy == nil || *params.SandboxPolicy != invalid || *params.Model != proposedModel {
		t.Fatal("admission changed caller input")
	}
}

func TestAcceptedLegacyPermissionSnapshotReachesNextTurn(t *testing.T) {
	threadRouter, record, _ := newPersistedResumeSettingsFixture(t)
	var current map[string]any
	if err := json.Unmarshal([]byte(`{"type":"managed","network":"restricted","file_system":{"type":"restricted","glob_scan_max_depth":2,"entries":[{"path":{"type":"glob_pattern","pattern":"**/*.env"},"access":"deny"}]}}`), &current); err != nil {
		t.Fatal(err)
	}
	record.Metadata.Extra["permission_profile"] = current
	if err := threadRouter.store.Save(record); err != nil {
		t.Fatal(err)
	}
	router := NewRuntimeRouter(RuntimeServices{ThreadRouter: threadRouter})
	defer router.Close()
	legacy := "read-only"
	if _, err := router.commitThreadSettingsUpdate(&SettingsUpdateParams{ThreadID: string(record.ID), SandboxPolicy: &legacy}, false); err != nil {
		t.Fatal(err)
	}
	settings := router.requireThreadExtras().Settings(string(record.ID))
	next := &turn.TurnStartParams{}
	applyThreadSettingsEnvironmentConfigToTurnStartParams(next, settings)
	resolved, err := threadDefaultSandboxPermissionProfile(&config.Config{Values: map[string]any{}}, record.Metadata.CWD, next)
	if err != nil {
		t.Fatal(err)
	}
	if resolved == nil || resolved.Profile == nil {
		t.Fatal("missing next-turn profile")
	}
	encoded, err := sandbox.RuntimePermissionProfileJSON(*resolved.Profile)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(encoded, `"access":"deny"`) || !strings.Contains(encoded, `"glob_scan_max_depth":2`) {
		t.Fatalf("next turn lost accepted restrictions: %s", encoded)
	}
	explicit := &turn.TurnStartParams{SandboxPolicy: "danger-full-access"}
	applyThreadSettingsEnvironmentConfigToTurnStartParams(explicit, settings)
	if explicit.PersistedPermissionProfile != nil {
		t.Fatal("accepted snapshot overrode explicit turn policy")
	}
}

func TestLegacyAdmissionPreservesUnmaterializedNamedDenials(t *testing.T) {
	threadRouter, record, cwd := newPersistedResumeSettingsFixture(t)
	denied := filepath.Join(cwd, "secret")
	record.Metadata.ActivePermissionProfile = "audit"
	record.Metadata.Extra["config"] = map[string]any{"permissions": map[string]any{"audit": map[string]any{"filesystem": map[string]any{":root": "read", denied: "deny"}}}}
	if err := threadRouter.store.Save(record); err != nil {
		t.Fatal(err)
	}
	router := NewRuntimeRouter(RuntimeServices{ThreadRouter: threadRouter, Config: config.NewConfigService(t.TempDir())})
	defer router.Close()
	legacy := "read-only"
	if _, err := router.commitThreadSettingsUpdate(&SettingsUpdateParams{ThreadID: string(record.ID), SandboxPolicy: &legacy}, false); err != nil {
		t.Fatal(err)
	}
	next := &turn.TurnStartParams{}
	applyThreadSettingsEnvironmentConfigToTurnStartParams(next, router.threadSettingsForTurn(string(record.ID)))
	if next.PersistedPermissionProfile == nil || !next.PersistedPermissionProfile.DeniesReadPath(denied) {
		t.Fatal("lost named-profile denial before first rollout materialization")
	}
}

func TestConsecutiveLegacyAdmissionsPreserveLiveDenials(t *testing.T) {
	threadRouter, record, cwd := newPersistedResumeSettingsFixture(t)
	denied := filepath.Join(cwd, "secret")
	record.Metadata.ActivePermissionProfile = "audit"
	record.Metadata.Extra["config"] = map[string]any{"permissions": map[string]any{"audit": map[string]any{"filesystem": map[string]any{":root": "read", denied: "deny"}}}}
	if err := threadRouter.store.Save(record); err != nil {
		t.Fatal(err)
	}
	router := NewRuntimeRouter(RuntimeServices{ThreadRouter: threadRouter, Config: config.NewConfigService(t.TempDir())})
	defer router.Close()
	legacy := "read-only"
	params := &SettingsUpdateParams{ThreadID: string(record.ID), SandboxPolicy: &legacy}
	if _, err := router.commitThreadSettingsUpdate(params, false); err != nil {
		t.Fatal(err)
	}
	// The old named configuration changes after the first accepted update.
	// The live anonymous profile, not that obsolete identity, is now authoritative.
	record.Metadata.Extra["config"] = map[string]any{"permissions": map[string]any{"audit": map[string]any{"filesystem": map[string]any{":root": "read"}}}}
	if err := threadRouter.store.Save(record); err != nil {
		t.Fatal(err)
	}
	legacy = "workspace-write"
	if _, err := router.commitThreadSettingsUpdate(params, false); err != nil {
		t.Fatal(err)
	}
	next := &turn.TurnStartParams{}
	applyThreadSettingsEnvironmentConfigToTurnStartParams(next, router.threadSettingsForTurn(string(record.ID)))
	if next.PersistedPermissionProfile == nil || !next.PersistedPermissionProfile.DeniesReadPath(denied) {
		t.Fatal("second update discarded live denial")
	}
}

func TestNamedPermissionAdmissionPreservesLiveDenialsLikeRust(t *testing.T) {
	threadRouter, record, cwd := newPersistedResumeSettingsFixture(t)
	denied := filepath.Join(cwd, "secret")
	record.Metadata.Extra["permission_profile"] = map[string]any{
		"type":    "managed",
		"network": "restricted",
		"file_system": map[string]any{
			"type": "restricted",
			"entries": []map[string]any{
				{"path": map[string]any{"type": "path", "path": denied}, "access": "deny"},
			},
		},
	}
	if err := threadRouter.store.Save(record); err != nil {
		t.Fatal(err)
	}
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: threadRouter,
		Config:       config.NewConfigService(t.TempDir()),
	})
	defer router.Close()
	legacy := "read-only"
	if _, err := router.commitThreadSettingsUpdate(&SettingsUpdateParams{ThreadID: string(record.ID), SandboxPolicy: &legacy}, false); err != nil {
		t.Fatal(err)
	}
	before, err := sandbox.ParseRuntimePermissionProfileJSON(string(router.threadSettingsForTurn(string(record.ID)).normalizedPermissionProfile))
	if err != nil {
		t.Fatal(err)
	}
	if !before.DeniesReadPath(denied) {
		t.Fatalf("legacy setup lost deny-read restriction for %s: %+v", denied, before.DeniedReadEntries)
	}
	named := sandbox.BuiltInPermissionProfileDangerFullAccess
	if _, err := router.commitThreadSettingsUpdate(&SettingsUpdateParams{ThreadID: string(record.ID), Permissions: &named}, false); err != nil {
		t.Fatal(err)
	}
	settings := router.threadSettingsForTurn(string(record.ID))
	profile, err := sandbox.ParseRuntimePermissionProfileJSON(string(settings.normalizedPermissionProfile))
	if err != nil {
		t.Fatal(err)
	}
	if !profile.DeniesReadPath(denied) {
		t.Fatalf("named admission lost deny-read restriction for %s: %+v", denied, profile.DeniedReadEntries)
	}
}
