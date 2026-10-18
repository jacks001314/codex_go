package appserver

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"codex_go/config"
	"codex_go/model"
	"codex_go/rollout"
	"codex_go/sandbox"
	"codex_go/session"
	"codex_go/turn"
)

// Rust #53419: a saved read-only profile is restored even when the current
// configured default is full access.
func TestPersistedPermissionProfileRestoresReadOnlyAgainstIncompatibleDefaultLikeRust(t *testing.T) {
	cwd := t.TempDir()
	cfg := &config.Config{Values: map[string]any{"default_permissions": ":" + sandbox.BuiltInPermissionProfileDangerFullAccess}}
	saved := sandbox.ReadOnlyPermissionProfile()
	params := &turn.TurnStartParams{CWD: cwd, PersistedPermissionProfile: &saved}

	resolution, err := threadDefaultSandboxPermissionProfile(cfg, cwd, params)
	if err != nil {
		t.Fatalf("threadDefaultSandboxPermissionProfile() error = %v", err)
	}
	if resolution == nil || resolution.Profile == nil || resolution.Profile.SandboxPolicy == nil ||
		resolution.Profile.SandboxPolicy.Kind != sandbox.SandboxReadOnly {
		t.Fatalf("restored profile = %#v, want read-only", resolution)
	}
	if strings.TrimSpace(resolution.ProfileJSON) == "" {
		t.Fatal("restored profile JSON is empty")
	}

	configured, err := threadDefaultSandboxPermissionProfile(cfg, cwd, &turn.TurnStartParams{CWD: cwd})
	if err != nil {
		t.Fatalf("configured default resolution error = %v", err)
	}
	if configured == nil || configured.Profile == nil || configured.Profile.SandboxPolicy == nil ||
		configured.Profile.SandboxPolicy.Kind != sandbox.SandboxDangerFullAccess {
		t.Fatalf("configured profile = %#v, want full access", configured)
	}
}

// An explicit permission selection wins over the saved fallback.
func TestPersistedPermissionProfileKeepsExplicitOverrideLikeRust(t *testing.T) {
	cwd := t.TempDir()
	cfg := &config.Config{Values: map[string]any{"default_permissions": ":" + sandbox.BuiltInPermissionProfileReadOnly}}
	saved := sandbox.ReadOnlyPermissionProfile()
	explicit := ":" + sandbox.BuiltInPermissionProfileDangerFullAccess
	params := &turn.TurnStartParams{
		CWD:                        cwd,
		Permissions:                &explicit,
		PersistedPermissionProfile: &saved,
	}

	resolution, err := threadDefaultSandboxPermissionProfile(cfg, cwd, params)
	if err != nil {
		t.Fatalf("threadDefaultSandboxPermissionProfile() error = %v", err)
	}
	if resolution == nil || resolution.ID != explicit || resolution.Profile == nil ||
		resolution.Profile.SandboxPolicy == nil || resolution.Profile.SandboxPolicy.Kind != sandbox.SandboxDangerFullAccess {
		t.Fatalf("explicit profile = %#v, want %q", resolution, explicit)
	}
}

// A managed allow-list keeps the configured default authoritative, so the
// saved profile is not eligible.
func TestPersistedPermissionProfileDefersToManagedAllowListLikeRust(t *testing.T) {
	cwd := t.TempDir()
	required := sandbox.BuiltInPermissionProfileDangerFullAccess
	cfg := &config.Config{
		Values: map[string]any{"default_permissions": ":" + required},
		Requirements: &config.ConfigRequirements{
			AllowedPermissionProfiles: map[string]bool{required: true},
			DefaultPermissions:        stringPointerForPersistedProfileTest(required),
		},
	}
	saved := sandbox.ReadOnlyPermissionProfile()
	resolution, err := threadDefaultSandboxPermissionProfile(cfg, cwd, &turn.TurnStartParams{
		CWD:                        cwd,
		PersistedPermissionProfile: &saved,
	})
	if err != nil {
		t.Fatalf("threadDefaultSandboxPermissionProfile() error = %v", err)
	}
	if resolution == nil || resolution.Profile == nil || resolution.Profile.SandboxPolicy == nil ||
		resolution.Profile.SandboxPolicy.Kind != sandbox.SandboxDangerFullAccess {
		t.Fatalf("managed profile = %#v, want the required full-access default", resolution)
	}
}

// Managed deny-read restrictions survive restoring a broader saved profile.
func TestPersistedPermissionProfilePreservesManagedDenyReadLikeRust(t *testing.T) {
	cwd := t.TempDir()
	denied := filepath.Join(cwd, "managed-secret")
	cfg := &config.Config{
		Requirements: &config.ConfigRequirements{
			Permissions: map[string]any{
				"filesystem": map[string]any{"deny_read": []any{denied}},
			},
		},
	}
	saved := sandbox.FullAccessPermissionProfile()
	resolution, err := threadDefaultSandboxPermissionProfile(cfg, cwd, &turn.TurnStartParams{
		CWD:                        cwd,
		PersistedPermissionProfile: &saved,
	})
	if err != nil {
		t.Fatalf("threadDefaultSandboxPermissionProfile() error = %v", err)
	}
	if resolution == nil || resolution.Profile == nil || !resolution.Profile.DeniesReadPath(denied) {
		t.Fatalf("restored profile = %#v, want managed deny-read for %s", resolution, denied)
	}
}

func TestPersistedPermissionProfileRejectsNonPortableSnapshotsLikeRust(t *testing.T) {
	readOnly := sandbox.ReadOnlyPermissionProfile()
	readOnlyJSON, err := sandbox.RuntimePermissionProfileJSON(readOnly)
	if err != nil {
		t.Fatalf("RuntimePermissionProfileJSON() error = %v", err)
	}
	var readOnlyValues map[string]any
	if err := json.Unmarshal([]byte(readOnlyJSON), &readOnlyValues); err != nil {
		t.Fatalf("Unmarshal(read-only) error = %v", err)
	}
	record := &session.Record{Metadata: session.Metadata{Extra: map[string]any{"permission_profile": readOnlyValues}}}
	params := &turn.TurnStartParams{}
	if profile := persistedPermissionProfileFromRecord(record, params); profile == nil ||
		profile.SandboxPolicy == nil || profile.SandboxPolicy.Kind != sandbox.SandboxReadOnly {
		t.Fatalf("recovered profile = %#v, want read-only", profile)
	}

	withActive := &turn.TurnStartParams{}
	activeRecord := &session.Record{Metadata: session.Metadata{
		ActivePermissionProfile: "workspace",
		Extra:                   map[string]any{"permission_profile": readOnlyValues},
	}}
	if profile := persistedPermissionProfileFromRecord(activeRecord, withActive); profile != nil {
		t.Fatalf("named profile record recovered unnamed profile %#v", profile)
	}
	withOverride := &turn.TurnStartParams{Config: map[string]any{"sandbox_workspace_write": map[string]any{"network_access": true}}}
	if profile := persistedPermissionProfileFromRecord(record, withOverride); profile != nil {
		t.Fatalf("sandbox_workspace_write override recovered unnamed profile %#v", profile)
	}

	external := map[string]any{"type": "external", "network": "enabled"}
	externalRecord := &session.Record{Metadata: session.Metadata{Extra: map[string]any{"permission_profile": external}}}
	if profile := persistedPermissionProfileFromRecord(externalRecord, &turn.TurnStartParams{}); profile != nil {
		t.Fatalf("external profile recovered %#v", profile)
	}

	materialized := map[string]any{
		"type":    "managed",
		"network": "restricted",
		"file_system": map[string]any{
			"type": "restricted",
			"entries": []any{map[string]any{
				"path":   map[string]any{"type": "path", "path": t.TempDir()},
				"access": "read",
			}},
		},
	}
	grantRecord := &session.Record{Metadata: session.Metadata{Extra: map[string]any{"permission_profile": materialized}}}
	if profile := persistedPermissionProfileFromRecord(grantRecord, &turn.TurnStartParams{}); profile != nil {
		t.Fatalf("materialized read grant recovered %#v", profile)
	}
}

// A Go-written turn context records the unnamed profile so a cold resume can
// recover it from the rollout.
func TestTurnContextRecordsPersistedPermissionProfileLikeRust(t *testing.T) {
	home := t.TempDir()
	store := session.NewStore(filepath.Join(home, "sessions"))
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		Config:       config.NewConfigService(home),
		Models: model.NewModelService(model.NewStaticModelsManager(model.ModelsResponse{Models: []model.ModelInfo{
			{Slug: "gpt-persisted-profile", ContextWindow: 200000},
		}})),
	})
	start := router.Handle(requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{CWD: home}))
	if start.Error != nil {
		t.Fatalf("thread start error: %+v", start.Error)
	}
	threadID := start.Result.(*ThreadStartResponse).Thread.ID
	record, err := store.Read(session.ThreadID(threadID), true, false)
	if err != nil {
		t.Fatalf("read thread error = %v", err)
	}
	readOnly := sandbox.ReadOnlyPermissionProfile()
	profileJSON, err := sandbox.RuntimePermissionProfileJSON(readOnly)
	if err != nil {
		t.Fatalf("RuntimePermissionProfileJSON() error = %v", err)
	}
	router.recordRuntimeTurnContext(threadID, "turn-profile", "turn-profile", &appTurnRunConfig{
		Model:                 "gpt-persisted-profile",
		SandboxPolicy:         string(sandbox.SandboxReadOnly),
		PermissionProfileJSON: profileJSON,
	}, record)

	cold, err := rollout.RecordFromPath(router.services.ThreadRouter.threadRolloutPath(record), false)
	if err != nil {
		t.Fatalf("RecordFromPath() error = %v", err)
	}
	recovered := persistedPermissionProfileFromRecord(cold, &turn.TurnStartParams{})
	if recovered == nil || recovered.SandboxPolicy == nil || recovered.SandboxPolicy.Kind != sandbox.SandboxReadOnly {
		t.Fatalf("recovered profile = %#v, want read-only", recovered)
	}
}

func stringPointerForPersistedProfileTest(value string) *string {
	return &value
}
