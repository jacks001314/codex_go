package appserver

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"codex_go/config"
	"codex_go/sandbox"
	"codex_go/session"
	"codex_go/turn"
)

func TestEnvironmentConfigStateParsingLikeRust(t *testing.T) {
	profile := sandbox.ReadOnlyPermissionProfile()
	profileJSON, err := sandbox.RuntimePermissionProfileJSON(profile)
	if err != nil {
		t.Fatal(err)
	}
	ready := map[string]any{
		"allow_login_shell":     true,
		"permission_profile":    profileJSON,
		"permission_profile_id": "read-only",
		"selectedCapabilityRoots": []any{
			map[string]any{"id": "root-1", "location": map[string]any{"type": "environment", "environmentId": "env-a", "path": "/remote"}},
		},
	}

	state, err := environmentConfigStateFromAny(map[string]any{"state": "ready", "config": ready})
	if err != nil {
		t.Fatalf("environmentConfigStateFromAny(ready) error = %v", err)
	}
	if state.Kind != EnvironmentConfigReady || state.Config == nil {
		t.Fatalf("ready state = %#v", state)
	}
	if !state.Config.AllowLoginShell || state.Config.ActivePermissionProfile != "read-only" {
		t.Fatalf("ready config = %#v", state.Config)
	}
	if state.Config.PermissionProfile == nil || !state.Config.PermissionProfile.Disabled && state.Config.PermissionProfile.SandboxPolicy == nil {
		t.Fatalf("ready permission profile = %#v", state.Config.PermissionProfile)
	}
	if len(state.Config.SelectedCapabilityRoots) != 1 || state.Config.SelectedCapabilityRoots[0].ID != "root-1" {
		t.Fatalf("ready roots = %#v", state.Config.SelectedCapabilityRoots)
	}

	if state, err := environmentConfigStateFromAny(map[string]any{"state": "pending"}); err != nil || state.Kind != EnvironmentConfigPending {
		t.Fatalf("pending = %#v, %v", state, err)
	}
	if state, err := environmentConfigStateFromAny(map[string]any{"state": "failed", "error": "boom"}); err != nil || state.Kind != EnvironmentConfigFailed || state.Error != "boom" {
		t.Fatalf("failed = %#v, %v", state, err)
	}
	if state, err := environmentConfigStateFromAny(map[string]any{"state": "from_thread"}); err != nil || state.Kind != EnvironmentConfigFromThread {
		t.Fatalf("from_thread = %#v, %v", state, err)
	}
	if state, err := environmentConfigStateFromAny(nil); err != nil || state.Kind != EnvironmentConfigFromThread {
		t.Fatalf("nil = %#v, %v", state, err)
	}
	// A bare config object is accepted as Ready.
	if state, err := environmentConfigStateFromAny(ready); err != nil || state.Kind != EnvironmentConfigReady {
		t.Fatalf("bare config = %#v, %v", state, err)
	}
}

func TestValidateEnvironmentSelectionConfigRejectsInvalidReadyRoots(t *testing.T) {
	valid := map[string]any{
		"state": "ready",
		"config": map[string]any{
			"allow_login_shell": false,
			"selectedCapabilityRoots": []any{
				map[string]any{"id": "root-1", "location": map[string]any{"type": "environment", "environmentId": "env-a", "path": "/remote"}},
			},
		},
	}
	normalized, err := validateEnvironmentSelectionConfig("env-a", map[string]any{"config": valid})
	if err != nil {
		t.Fatalf("validateEnvironmentSelectionConfig(valid) error = %v", err)
	}
	if normalized == nil || normalized["state"] != string(EnvironmentConfigReady) {
		t.Fatalf("normalized = %#v", normalized)
	}

	wrongEnv := cloneAnyMapForRouter(valid)
	wrongEnv["config"] = cloneAnyMapForRouter(valid["config"].(map[string]any))
	wrongEnv["config"].(map[string]any)["selectedCapabilityRoots"] = []any{
		map[string]any{"id": "root-1", "location": map[string]any{"type": "environment", "environmentId": "other-env", "path": "/remote"}},
	}
	if _, err := validateEnvironmentSelectionConfig("env-a", map[string]any{"config": wrongEnv}); err == nil || !strings.Contains(err.Error(), "belong to environment") {
		t.Fatalf("wrong environment error = %v", err)
	}

	duplicate := cloneAnyMapForRouter(valid)
	duplicate["config"] = cloneAnyMapForRouter(valid["config"].(map[string]any))
	duplicate["config"].(map[string]any)["selectedCapabilityRoots"] = []any{
		map[string]any{"id": "root-1", "location": map[string]any{"type": "environment", "environmentId": "env-a", "path": "/remote"}},
		map[string]any{"id": "root-1", "location": map[string]any{"type": "environment", "environmentId": "env-a", "path": "/other"}},
	}
	if _, err := validateEnvironmentSelectionConfig("env-a", map[string]any{"config": duplicate}); err == nil || !strings.Contains(err.Error(), "unique non-empty IDs") {
		t.Fatalf("duplicate error = %v", err)
	}

	if _, err := validateEnvironmentSelectionConfig("env-a", map[string]any{"config": map[string]any{"state": "failed"}}); err == nil || !strings.Contains(err.Error(), "requires an error") {
		t.Fatalf("failed-without-error error = %v", err)
	}
	if _, err := validateEnvironmentSelectionConfig("env-a", map[string]any{"config": map[string]any{"state": "bogus"}}); err == nil || !strings.Contains(err.Error(), "unknown environment configuration state") {
		t.Fatalf("bogus state error = %v", err)
	}
}

func TestResolveEnvironmentConfigTracksOwnershipLikeRust(t *testing.T) {
	threadConfig := &EnvironmentConfig{AllowLoginShell: true, ActivePermissionProfile: "workspace"}
	ownerConfig := &EnvironmentConfig{AllowLoginShell: false, ActivePermissionProfile: "read-only"}

	fromThread := map[string]any{"environmentId": "env-a", "cwd": "/remote"}
	state, origin := resolveEnvironmentConfig(fromThread, threadConfig)
	if origin != EnvironmentConfigOriginThread || state.Kind != EnvironmentConfigReady || state.Config == nil || !state.Config.AllowLoginShell {
		t.Fatalf("fromThread resolution = %#v origin=%s", state, origin)
	}
	if state.Config == threadConfig {
		t.Fatal("thread config must be cloned, not aliased")
	}

	ownerReady := map[string]any{"environmentId": "env-a", "cwd": "/remote", "config": environmentConfigStateToAny(EnvironmentConfigState{Kind: EnvironmentConfigReady, Config: ownerConfig})}
	state, origin = resolveEnvironmentConfig(ownerReady, threadConfig)
	if origin != EnvironmentConfigOriginOwner || state.Kind != EnvironmentConfigReady || state.Config.AllowLoginShell {
		t.Fatalf("owner ready resolution = %#v origin=%s", state, origin)
	}

	pending := map[string]any{"environmentId": "env-a", "cwd": "/remote", "config": environmentConfigStateToAny(EnvironmentConfigState{Kind: EnvironmentConfigPending})}
	state, origin = resolveEnvironmentConfig(pending, threadConfig)
	if origin != EnvironmentConfigOriginOwner || state.Kind != EnvironmentConfigPending {
		t.Fatalf("owner pending resolution = %#v origin=%s", state, origin)
	}
}

func TestValidateTurnEnvironmentSelectionsAcceptsConfigStates(t *testing.T) {
	manager := NewEnvironmentManager(EnvironmentShellInfo{Name: "fake", Path: "fake-shell"}, t.TempDir())
	if _, err := manager.Add(&EnvironmentAddParams{EnvironmentID: "env-a", ExecServerURL: "ws://127.0.0.1:1234"}); err != nil {
		t.Fatal(err)
	}
	router := NewRuntimeRouter(RuntimeServices{Environment: manager})
	defer router.Close()

	environments := []map[string]any{
		{"environmentId": "env-a", "cwd": "/remote"},
		{"environmentId": "env-a", "cwd": "/remote", "config": map[string]any{"state": "pending"}},
		{"environmentId": "env-a", "cwd": "/remote", "config": map[string]any{"state": "failed", "error": "provisioning failed"}},
	}
	if err := router.validateTurnEnvironmentSelections(environments); err != nil {
		t.Fatalf("validateTurnEnvironmentSelections() error = %v", err)
	}
	if len(environments) != 3 {
		t.Fatalf("environments = %#v", environments)
	}
	if environments[0]["config"] == nil || environments[0]["config"].(map[string]any)["state"] != string(EnvironmentConfigFromThread) {
		t.Fatalf("normalized from_thread config = %#v", environments[0]["config"])
	}
	if environments[1]["config"].(map[string]any)["state"] != string(EnvironmentConfigPending) {
		t.Fatalf("pending config = %#v", environments[1]["config"])
	}
	if environments[2]["config"].(map[string]any)["state"] != string(EnvironmentConfigFailed) {
		t.Fatalf("failed config = %#v", environments[2]["config"])
	}
}

func TestUnifiedExecEnvironmentsForTurnSkipsPendingFailedAndAppliesConfig(t *testing.T) {
	manager := NewEnvironmentManager(EnvironmentShellInfo{Name: "fake", Path: "fake-shell"}, t.TempDir())
	if _, err := manager.Add(&EnvironmentAddParams{EnvironmentID: "ready-env", ExecServerURL: "ws://127.0.0.1:1234"}); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Add(&EnvironmentAddParams{EnvironmentID: "pending-env", ExecServerURL: "ws://127.0.0.1:1235"}); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Add(&EnvironmentAddParams{EnvironmentID: "failed-env", ExecServerURL: "ws://127.0.0.1:1236"}); err != nil {
		t.Fatal(err)
	}
	readOnly := sandbox.ReadOnlyPermissionProfile()
	profileJSON, err := sandbox.RuntimePermissionProfileJSON(readOnly)
	if err != nil {
		t.Fatal(err)
	}
	router := NewRuntimeRouter(RuntimeServices{Environment: manager})
	defer router.Close()

	environments := router.unifiedExecEnvironmentsForTurn(&turn.TurnStartParams{Environments: []map[string]any{
		{"environmentId": "ready-env", "cwd": "/remote", "config": map[string]any{"state": "ready", "config": map[string]any{
			"allow_login_shell":     false,
			"permission_profile":    profileJSON,
			"permission_profile_id": "read-only",
			"shell_environment_policy": map[string]any{
				"inherit": "core",
				"set":     map[string]any{"CODEX_ENV": "ready"},
			},
		}}},
		{"environmentId": "pending-env", "cwd": "/remote", "config": map[string]any{"state": "pending"}},
		{"environmentId": "failed-env", "cwd": "/remote", "config": map[string]any{"state": "failed", "error": "boom"}},
	}})
	if len(environments) != 1 {
		t.Fatalf("environments = %#v", environments)
	}
	if environments[0].ID != "ready-env" {
		t.Fatalf("environment id = %q", environments[0].ID)
	}
	if environments[0].AllowLoginShell == nil || *environments[0].AllowLoginShell {
		t.Fatalf("allow login shell = %#v, want false", environments[0].AllowLoginShell)
	}
	if environments[0].PermissionProfile == nil || environments[0].PermissionProfileID != "read-only" {
		t.Fatalf("permission profile = %#v id=%q", environments[0].PermissionProfile, environments[0].PermissionProfileID)
	}
	// #38902: the resolved environment's shell environment policy is carried
	// on the turn environment so shell/unified-exec apply it.
	if len(environments[0].ShellEnvironmentPolicy) == 0 {
		t.Fatalf("ready environment shell environment policy not carried: %#v", environments[0])
	}
	if inherit, _ := environments[0].ShellEnvironmentPolicy["inherit"].(string); inherit != "core" {
		t.Fatalf("ready environment policy = %#v, want inherit=core", environments[0].ShellEnvironmentPolicy)
	}
}

// TestThreadEnvironmentConfigForTurnPopulatesShellPolicyLikeRust verifies the
// inferred (thread-derived) shell environment policy: the thread's
// EnvironmentConfig carries the config's shell_environment_policy so
// environments without their own resolved policy inherit it (Rust
// inferred_environment_config, #38902).
func TestThreadEnvironmentConfigForTurnPopulatesShellPolicyLikeRust(t *testing.T) {
	home := t.TempDir()
	codexHome := filepath.Join(home, "codex")
	if err := os.MkdirAll(codexHome, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(config.ConfigPath(codexHome), []byte("[shell_environment_policy]\ninherit = \"core\"\n\n[shell_environment_policy.set]\nCODEX_THREAD = \"inferred\"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile config: %v", err)
	}
	router := NewRuntimeRouter(RuntimeServices{Config: config.NewConfigService(codexHome)})
	defer router.Close()

	cfg := router.threadEnvironmentConfigForTurn(&turn.TurnStartParams{CWD: t.TempDir()})
	if cfg == nil {
		t.Fatalf("thread environment config = nil")
	}
	if len(cfg.ShellEnvironmentPolicy) == 0 {
		t.Fatalf("thread environment config carries no shell environment policy: %#v", cfg)
	}
	if inherit, _ := cfg.ShellEnvironmentPolicy["inherit"].(string); inherit != "core" {
		t.Fatalf("thread policy inherit = %#v, want core", cfg.ShellEnvironmentPolicy)
	}
	set, _ := cfg.ShellEnvironmentPolicy["set"].(map[string]any)
	if set["CODEX_THREAD"] != "inferred" {
		t.Fatalf("thread policy set = %#v", cfg.ShellEnvironmentPolicy)
	}
}

// TestEnvironmentConfigShellPolicyJSONRoundTrip pins the serialized surface of
// the resolved shell environment policy: it survives the environment-config
// JSON roundtrip (thread metadata persistence) with nested set/filters maps
// intact.
func TestEnvironmentConfigShellPolicyJSONRoundTrip(t *testing.T) {
	config := &EnvironmentConfig{
		AllowLoginShell: true,
		ShellEnvironmentPolicy: map[string]any{
			"inherit": "core",
			"set":     map[string]any{"CODEX_KEPT": "yes"},
			"filters": map[string]any{"PATH": "include"},
		},
	}
	serialized := environmentConfigToAny(config)
	if _, present := serialized["shell_environment_policy"]; !present {
		t.Fatalf("shell_environment_policy not serialized: %#v", serialized)
	}
	parsed, err := environmentConfigFromAny(serialized)
	if err != nil {
		t.Fatalf("environmentConfigFromAny: %v", err)
	}
	if parsed == nil || len(parsed.ShellEnvironmentPolicy) == 0 {
		t.Fatalf("parsed policy missing: %#v", parsed)
	}
	if inherit, _ := parsed.ShellEnvironmentPolicy["inherit"].(string); inherit != "core" {
		t.Fatalf("parsed inherit = %#v", parsed.ShellEnvironmentPolicy)
	}
	set, _ := parsed.ShellEnvironmentPolicy["set"].(map[string]any)
	if set["CODEX_KEPT"] != "yes" {
		t.Fatalf("parsed set = %#v", parsed.ShellEnvironmentPolicy)
	}
	filters, _ := parsed.ShellEnvironmentPolicy["filters"].(map[string]any)
	if filters["PATH"] != "include" {
		t.Fatalf("parsed filters = %#v", parsed.ShellEnvironmentPolicy)
	}
}

func TestInspectSelectedCapabilityRootsIncludesReadyAttachmentConfig(t *testing.T) {
	now := time.Now().UTC()
	threadRoots := []json.RawMessage{mustJSONRaw(t, map[string]any{
		"id": "thread-root", "location": map[string]any{"type": "environment", "environmentId": "local", "path": "/thread"},
	})}
	record := &session.Record{ID: "thread-1", CreatedAt: now, UpdatedAt: now, RecencyAt: now, Metadata: session.Metadata{
		SelectedCapabilityRoots: threadRoots,
		Extra: map[string]any{runtimeEnvironmentSelectionsExtraKey: []map[string]any{
			{"environmentId": "env-a", "cwd": "/remote", "config": map[string]any{"state": "ready", "config": map[string]any{
				"selectedCapabilityRoots": []any{
					map[string]any{"id": "attachment-root", "location": map[string]any{"type": "environment", "environmentId": "env-a", "path": "/attachment"}},
				},
			}}},
			{"environmentId": "env-b", "cwd": "/remote", "config": map[string]any{"state": "pending"}},
		}},
	}}
	router := NewRuntimeRouter(RuntimeServices{})
	status := router.inspectSelectedCapabilityRootsForThread(record)
	ids := make([]string, 0, len(status.ReadyRoots))
	for _, root := range status.ReadyRoots {
		ids = append(ids, root.ID)
	}
	if want := []string{"thread-root", "attachment-root"}; !reflect.DeepEqual(ids, want) {
		t.Fatalf("ready roots = %#v, want %#v", ids, want)
	}
}

func TestEnvironmentConfigJSONRoundTrip(t *testing.T) {
	config := &EnvironmentConfig{
		AllowLoginShell:         false,
		ActivePermissionProfile: "read-only",
		SelectedCapabilityRoots: []SelectedCapabilityRoot{
			{ID: "root-1", Location: CapabilityRootLocation{Type: CapabilityRootLocationEnvironment, EnvironmentID: "env-a", Path: "/remote"}},
		},
	}
	wire := environmentConfigToAny(config)
	parsed, err := environmentConfigFromAny(wire)
	if err != nil {
		t.Fatalf("environmentConfigFromAny() error = %v", err)
	}
	if parsed.AllowLoginShell != config.AllowLoginShell || parsed.ActivePermissionProfile != config.ActivePermissionProfile {
		t.Fatalf("parsed = %#v", parsed)
	}
	if len(parsed.SelectedCapabilityRoots) != 1 || parsed.SelectedCapabilityRoots[0].ID != "root-1" {
		t.Fatalf("parsed roots = %#v", parsed.SelectedCapabilityRoots)
	}
}

func mustJSONRaw(t *testing.T, value any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
