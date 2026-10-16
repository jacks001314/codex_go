package mcp

import (
	"encoding/json"
	"reflect"
	"testing"
)

// TestAugmentSandboxMetadataWritesBothStateKeysLikeRust mirrors
// codex-rs/codex-mcp/src/sandbox_metadata_tests.rs + binding_tests.rs: the
// policy is written under both state keys with identical values, unrelated
// entries survive, and the launcher envelope appears only when the host
// verified an executable.
func TestAugmentSandboxMetadataWritesBothStateKeysLikeRust(t *testing.T) {
	meta := map[string]any{
		"unrelated":       "keep-me",
		"codexExecutable": "must-not-survive",
	}
	state := &SandboxState{
		PermissionProfile: json.RawMessage(`{"type":"managed","future_field":{"preserved":true}}`),
		SandboxCWD:        "/home/user/project",
		UseMXC:            true,
	}

	out, ok := augmentSandboxMetadata(meta, state, "/opt/codex/bin/codex").(map[string]any)
	if !ok {
		t.Fatal("augmented meta should be an object")
	}
	if out["unrelated"] != "keep-me" {
		t.Errorf("unrelated entry was dropped: %#v", out)
	}
	current, hasCurrent := out[mcpSandboxStateCapability]
	legacy, hasLegacy := out[mcpSandboxStateMetaCapability]
	if !hasCurrent || !hasLegacy {
		t.Fatalf("both state keys are required: %#v", out)
	}
	if !reflect.DeepEqual(current, legacy) {
		t.Errorf("state aliases differ:\ncurrent = %#v\nlegacy  = %#v", current, legacy)
	}
	launcher, ok := out[mcpSandboxLauncherMetaKey].(map[string]any)
	if !ok {
		t.Fatalf("verified executable must produce a launcher envelope: %#v", out)
	}
	if launcher[mcpSandboxLauncherExecutableField] != "/opt/codex/bin/codex" {
		t.Errorf("launcher executable = %v", launcher[mcpSandboxLauncherExecutableField])
	}
	if len(launcher) != 1 {
		t.Errorf("launcher carries only codexExecutable: %#v", launcher)
	}

	stateMap := current.(map[string]any)
	if stateMap["sandboxCwd"] != "/home/user/project" {
		t.Errorf("sandbox cwd = %v", stateMap["sandboxCwd"])
	}
	if stateMap["useMxc"] != true {
		t.Errorf("useMxc = %v", stateMap["useMxc"])
	}
	profile, ok := stateMap["permissionProfile"].(map[string]any)
	if !ok {
		t.Fatalf("permission profile must stay an opaque object: %#v", stateMap["permissionProfile"])
	}
	future, ok := profile["future_field"].(map[string]any)
	if !ok || future["preserved"] != true {
		t.Errorf("unknown policy fields must survive unchanged: %#v", profile)
	}
	if _, exists := stateMap["codexExecutable"]; exists {
		t.Errorf("launcher data must not leak into the policy object: %#v", stateMap)
	}
}

// TestAugmentSandboxMetadataRemovesStaleLauncher covers Rust's
// `metadata.remove(MCP_SANDBOX_LAUNCHER_META_KEY)`: a reserved launcher entry
// from an earlier call must not survive when this environment has no verified
// executable.
func TestAugmentSandboxMetadataRemovesStaleLauncher(t *testing.T) {
	meta := map[string]any{
		mcpSandboxLauncherMetaKey: map[string]any{mcpSandboxLauncherExecutableField: "/stale/codex"},
		mcpSandboxStateCapability: map[string]any{"permissionProfile": map[string]any{"type": "disabled"}},
	}
	state := &SandboxState{PermissionProfile: json.RawMessage(`{"type":"disabled"}`), SandboxCWD: "/tmp/work"}

	out, ok := augmentSandboxMetadata(meta, state, "").(map[string]any)
	if !ok {
		t.Fatal("augmented meta should be an object")
	}
	if _, exists := out[mcpSandboxLauncherMetaKey]; exists {
		t.Fatalf("stale launcher metadata survived: %#v", out)
	}
	stateMap, ok := out[mcpSandboxStateCapability].(map[string]any)
	if !ok || stateMap["sandboxCwd"] != "/tmp/work" {
		t.Fatalf("policy must be replaced with the current state: %#v", out)
	}
}

func TestAugmentSandboxMetadataReplacesExistingLauncher(t *testing.T) {
	meta := map[string]any{
		mcpSandboxLauncherMetaKey: map[string]any{mcpSandboxLauncherExecutableField: "/old/codex"},
	}
	state := &SandboxState{PermissionProfile: json.RawMessage(`{"type":"disabled"}`), SandboxCWD: "/tmp/work"}

	out := augmentSandboxMetadata(meta, state, "/new/codex").(map[string]any)
	launcher := out[mcpSandboxLauncherMetaKey].(map[string]any)
	if launcher[mcpSandboxLauncherExecutableField] != "/new/codex" {
		t.Fatalf("launcher was not replaced: %#v", launcher)
	}
}

// TestAugmentSandboxMetadataLeavesNonObjectMetaUntouched mirrors Rust returning
// early when `_meta` is present but is not a JSON object.
func TestAugmentSandboxMetadataLeavesNonObjectMetaUntouched(t *testing.T) {
	state := &SandboxState{PermissionProfile: json.RawMessage(`{"type":"disabled"}`), SandboxCWD: "/tmp/work"}

	if got := augmentSandboxMetadata("not-an-object", state, ""); got != "not-an-object" {
		t.Fatalf("non-object meta = %#v, want it unchanged", got)
	}
	out, ok := augmentSandboxMetadata(nil, state, "").(map[string]any)
	if !ok {
		t.Fatal("absent meta must be created as an object")
	}
	if _, exists := out[mcpSandboxStateMetaCapability]; !exists {
		t.Fatalf("absent meta must receive both state keys: %#v", out)
	}
}

// TestCheckSandboxStateMetaCapabilityAcceptsBothSpellings mirrors Rust #53709's
// rmcp_client gate: the experimental `codex/sandbox-state` capability is
// accepted alongside the deprecated `codex/sandbox-state-meta` alias.
func TestCheckSandboxStateMetaCapabilityAcceptsBothSpellings(t *testing.T) {
	cases := []struct {
		name   string
		result string
		want   bool
	}{
		{name: "experimental state capability", result: `{"capabilities":{"experimental":{"codex/sandbox-state":{}}}}`, want: true},
		{name: "deprecated meta alias", result: `{"capabilities":{"experimental":{"codex/sandbox-state-meta":{}}}}`, want: true},
		{name: "unknown capability only", result: `{"capabilities":{"experimental":{"codex/tool-catalog-cache":{}}}}`, want: false},
		{name: "no experimental block", result: `{"capabilities":{}}`, want: false},
		{name: "unparsable result", result: `{`, want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw := json.RawMessage(tc.result)
			if got := checkSandboxStateMetaCapability(&raw); got != tc.want {
				t.Fatalf("checkSandboxStateMetaCapability(%s) = %t, want %t", tc.result, got, tc.want)
			}
		})
	}
	if checkSandboxStateMetaCapability(nil) {
		t.Fatal("a nil initialize result must not advertise the capability")
	}
}
