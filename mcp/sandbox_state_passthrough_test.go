package mcp

import (
	"encoding/json"
	"testing"
)

// workspaceWriteProfileJSON is an opaque policy document with the shape the
// sandbox CLI consumes through --sandbox-state-json. The MCP layer must forward
// it verbatim, so the tests assert on the encoded object rather than on a
// profile label.
const workspaceWriteProfileJSON = `{"type":"managed","file_system":{"type":"unrestricted","future_policy_field":true},"network":"enabled"}`

func TestAugmentToolCallMetaWithSandboxState(t *testing.T) {
	service := NewMCPService(nil)

	params := &MCPToolCallParams{
		ThreadID:                 "thread-1",
		SupportsSandboxStateMeta: true,
		PermissionProfile:        workspaceWriteProfileJSON,
		SandboxCWD:               "/home/user/project",
		CodexLinuxSandboxExe:     "/usr/bin/codex-linux-sandbox",
		UseLegacyLandlock:        false,
	}

	meta := service.augmentToolCallMeta(params)

	metaMap, ok := meta.(map[string]any)
	if !ok {
		t.Fatal("expected meta to be map[string]any")
	}

	if metaMap[mcpToolThreadIDMetaKey] != "thread-1" {
		t.Errorf("thread ID = %v", metaMap[mcpToolThreadIDMetaKey])
	}

	sandboxState, ok := metaMap[mcpSandboxStateMetaCapability]
	if !ok {
		t.Fatal("expected sandbox state in meta")
	}

	stateMap, ok := sandboxState.(map[string]any)
	if !ok {
		t.Fatal("expected sandbox state to be map")
	}

	profile, ok := stateMap["permissionProfile"].(map[string]any)
	if !ok || profile["type"] != "managed" {
		t.Errorf("permission profile = %#v", stateMap["permissionProfile"])
	}
	fileSystem, ok := profile["file_system"].(map[string]any)
	if !ok || fileSystem["future_policy_field"] != true {
		t.Errorf("unknown policy fields must survive the handoff: %#v", profile["file_system"])
	}
	if stateMap["sandboxCwd"] != "/home/user/project" {
		t.Errorf("sandbox cwd = %v", stateMap["sandboxCwd"])
	}
	if stateMap["codexLinuxSandboxExe"] != "/usr/bin/codex-linux-sandbox" {
		t.Errorf("linux sandbox exe = %v", stateMap["codexLinuxSandboxExe"])
	}
	if _, exists := stateMap["useLegacyLandlock"]; exists {
		t.Errorf("useLegacyLandlock should be omitted when false, got %v", stateMap["useLegacyLandlock"])
	}
}

func TestAugmentToolCallMetaSkipsWhenCapabilityNotSupported(t *testing.T) {
	service := NewMCPService(nil)

	params := &MCPToolCallParams{
		ThreadID:                 "thread-1",
		SupportsSandboxStateMeta: false,
		PermissionProfile:        workspaceWriteProfileJSON,
		SandboxCWD:               "/home/user/project",
	}

	meta := service.augmentToolCallMeta(params)

	metaMap, ok := meta.(map[string]any)
	if !ok {
		t.Fatal("expected meta to be map[string]any")
	}

	if _, exists := metaMap[mcpSandboxStateMetaCapability]; exists {
		t.Error("sandbox state should not be in meta when capability not supported")
	}

	if metaMap[mcpToolThreadIDMetaKey] != "thread-1" {
		t.Errorf("thread ID = %v", metaMap[mcpToolThreadIDMetaKey])
	}
}

func TestAugmentToolCallMetaSkipsWhenCWDEmpty(t *testing.T) {
	service := NewMCPService(nil)

	params := &MCPToolCallParams{
		ThreadID:                 "thread-1",
		SupportsSandboxStateMeta: true,
		PermissionProfile:        workspaceWriteProfileJSON,
		SandboxCWD:               "",
	}

	meta := service.augmentToolCallMeta(params)

	metaMap, ok := meta.(map[string]any)
	if !ok {
		t.Fatal("expected meta to be map[string]any")
	}

	if _, exists := metaMap[mcpSandboxStateMetaCapability]; exists {
		t.Error("sandbox state should not be in meta when CWD is empty")
	}
}

func TestAugmentToolCallMetaPreservesExistingMeta(t *testing.T) {
	service := NewMCPService(nil)

	existingMeta := map[string]any{
		"custom_field": "custom_value",
	}

	params := &MCPToolCallParams{
		ThreadID:                 "thread-1",
		Meta:                     existingMeta,
		SupportsSandboxStateMeta: true,
		PermissionProfile:        workspaceWriteProfileJSON,
		SandboxCWD:               "/home/user/project",
	}

	meta := service.augmentToolCallMeta(params)

	metaMap, ok := meta.(map[string]any)
	if !ok {
		t.Fatal("expected meta to be map[string]any")
	}

	if metaMap["custom_field"] != "custom_value" {
		t.Error("existing meta field was not preserved")
	}

	if metaMap[mcpToolThreadIDMetaKey] != "thread-1" {
		t.Error("thread ID was not added")
	}

	if _, exists := metaMap[mcpSandboxStateMetaCapability]; !exists {
		t.Error("sandbox state was not added")
	}
}

func TestAugmentToolCallMetaSandboxStateJSONShape(t *testing.T) {
	service := NewMCPService(nil)

	params := &MCPToolCallParams{
		ThreadID:                 "thread-1",
		SupportsSandboxStateMeta: true,
		PermissionProfile:        workspaceWriteProfileJSON,
		SandboxCWD:               "/home/user/project",
		CodexLinuxSandboxExe:     "/usr/bin/codex-linux-sandbox",
		UseLegacyLandlock:        true,
	}

	meta := service.augmentToolCallMeta(params)

	encoded, err := json.Marshal(meta)
	if err != nil {
		t.Fatal(err)
	}

	var decoded map[string]any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}

	sandboxState := decoded[mcpSandboxStateMetaCapability].(map[string]any)
	profile := sandboxState["permissionProfile"].(map[string]any)
	if profile["type"] != "managed" {
		t.Errorf("permission profile after round trip = %v", profile["type"])
	}
	if sandboxState["sandboxCwd"] != "/home/user/project" {
		t.Errorf("sandbox cwd after round trip = %v", sandboxState["sandboxCwd"])
	}
	if sandboxState["useLegacyLandlock"] != true {
		t.Errorf("legacy landlock after round trip = %v", sandboxState["useLegacyLandlock"])
	}
}
