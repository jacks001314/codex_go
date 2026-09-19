package agent

import (
	"encoding/json"
	"strings"
	"testing"

	"codex_go/tool"
)

// registerV2WithOverrides registers the V2 handlers with the supplied catalog
// overrides and returns the registry.
func registerV2WithOverrides(t *testing.T, overrides map[string]MultiAgentToolOverride) *tool.Registry {
	t.Helper()
	registry := tool.NewRegistry()
	if err := RegisterMultiAgentHandlersWithOptions(registry, &MultiAgentHandlerOptions{
		Controller:    &limitV2Controller{MemoryToolController: NewMemoryToolController()},
		Version:       VersionV2,
		Exposure:      tool.ExposureModelVisible,
		ToolOverrides: overrides,
	}); err != nil {
		t.Fatalf("RegisterMultiAgentHandlersWithOptions() error = %v", err)
	}
	return registry
}

// TestMultiAgentV2CatalogOverridesApplyLikeRust mirrors Rust #46505: a valid
// catalog parameter schema replaces the bundled one, a catalog description
// replaces the bundled text for every V2 tool except spawn_agent, and invalid
// overrides fall back to the bundled spec.
func TestMultiAgentV2CatalogOverridesApplyLikeRust(t *testing.T) {
	description := "Catalog send_message description."
	parameters := `{"type":"object","properties":{"target":{"type":"string","description":"Catalog target."},"message":{"type":"string"},"custom":{"type":"boolean"}},"required":["target","message"]}`
	registry := registerV2WithOverrides(t, map[string]MultiAgentToolOverride{
		"send_message": {Description: &description, Parameters: &parameters},
	})
	send, ok := registry.Lookup(tool.NamespacedName(MultiAgentV2Namespace, "send_message"))
	if !ok {
		t.Fatal("send_message was not registered")
	}
	spec := send.Spec()
	if spec.Description != description {
		t.Fatalf("description = %q, want the catalog override", spec.Description)
	}
	properties, _ := spec.InputSchema["properties"].(map[string]any)
	if len(properties) != 3 || properties["custom"] == nil {
		t.Fatalf("input schema = %#v, want the catalog parameters", spec.InputSchema)
	}
	required, _ := spec.InputSchema["required"].([]any)
	if len(required) != 2 {
		t.Fatalf("required = %#v", spec.InputSchema["required"])
	}
	// Rust restores the harness encryption marker on the bundled encrypted
	// property even when the catalog override omitted it.
	message, _ := properties["message"].(map[string]any)
	if encrypted, _ := message["encrypted"].(bool); !encrypted {
		t.Fatalf("encrypted marker was not restored: %#v", message)
	}
}

// TestMultiAgentV2SpawnEmptyDescriptionSuppressesStaticTextLikeRust covers Rust
// #46297's empty-string rule: an explicit empty description suppresses the
// bundled static text without disabling the tool, and the usage hint survives.
func TestMultiAgentV2SpawnEmptyDescriptionSuppressesStaticTextLikeRust(t *testing.T) {
	empty := ""
	hint := "Configured usage hint."
	registry := tool.NewRegistry()
	if err := RegisterMultiAgentHandlersWithOptions(registry, &MultiAgentHandlerOptions{
		Controller:    &limitV2Controller{MemoryToolController: NewMemoryToolController()},
		Version:       VersionV2,
		Exposure:      tool.ExposureModelVisible,
		UsageHintText: &hint,
		ToolOverrides: map[string]MultiAgentToolOverride{
			"spawn_agent": {Description: &empty},
		},
	}); err != nil {
		t.Fatalf("RegisterMultiAgentHandlersWithOptions() error = %v", err)
	}
	spawn, ok := registry.Lookup(tool.NamespacedName(MultiAgentV2Namespace, "spawn_agent"))
	if !ok {
		t.Fatal("spawn_agent was not registered")
	}
	got := spawn.Spec().Description
	if strings.Contains(got, "Spawns an agent to work on the specified task.") {
		t.Fatalf("spawn_agent description = %q, want the bundled text suppressed", got)
	}
	if !strings.HasSuffix(got, hint) {
		t.Fatalf("spawn_agent description = %q, want the usage hint retained", got)
	}
}

// TestMultiAgentV2SpawnReplacesStaticDescriptionLikeRust covers Rust #46297's
// spawn_agent half: the catalog description replaces the bundled static text,
// the configured usage hint is still appended, and the parameter override still
// applies.
func TestMultiAgentV2SpawnReplacesStaticDescriptionLikeRust(t *testing.T) {
	description := "Catalog spawn description."
	hint := "Configured usage hint."
	parameters := `{"type":"object","properties":{"task_name":{"type":"string"},"message":{"type":"string","encrypted":true}},"required":["task_name","message"]}`
	registry := tool.NewRegistry()
	if err := RegisterMultiAgentHandlersWithOptions(registry, &MultiAgentHandlerOptions{
		Controller:    &limitV2Controller{MemoryToolController: NewMemoryToolController()},
		Version:       VersionV2,
		Exposure:      tool.ExposureModelVisible,
		UsageHintText: &hint,
		ToolOverrides: map[string]MultiAgentToolOverride{
			"spawn_agent": {Description: &description, Parameters: &parameters},
		},
	}); err != nil {
		t.Fatalf("RegisterMultiAgentHandlersWithOptions() error = %v", err)
	}
	spawn, ok := registry.Lookup(tool.NamespacedName(MultiAgentV2Namespace, "spawn_agent"))
	if !ok {
		t.Fatal("spawn_agent was not registered")
	}
	spec := spawn.Spec()
	if !strings.Contains(spec.Description, description) {
		t.Fatalf("spawn_agent description = %q, want the catalog override", spec.Description)
	}
	if strings.Contains(spec.Description, "Spawns an agent to work on the specified task.") {
		t.Fatalf("spawn_agent description = %q, want the bundled text replaced", spec.Description)
	}
	if !strings.HasSuffix(spec.Description, hint) {
		t.Fatalf("spawn_agent description = %q, want the usage hint appended", spec.Description)
	}
	properties, _ := spec.InputSchema["properties"].(map[string]any)
	if _, ok := properties["message"]; !ok || len(properties) != 2 {
		t.Fatalf("spawn_agent input schema = %#v, want the catalog parameters", spec.InputSchema)
	}
	message, _ := properties["message"].(map[string]any)
	if encrypted, _ := message["encrypted"].(bool); !encrypted {
		t.Fatalf("encrypted marker was not restored: %#v", message)
	}
}

// TestMultiAgentV2InvalidCatalogOverridesFallBackLikeRust mirrors Rust's
// validation: a non-object root, malformed JSON, an unsupported structure, or
// an override that drops a bundled encrypted property keeps the bundled
// parameters.
func TestMultiAgentV2InvalidCatalogOverridesFallBackLikeRust(t *testing.T) {
	for _, testCase := range []struct {
		name       string
		parameters string
	}{
		{name: "not json", parameters: `{"type":"object"`},
		{name: "not an object", parameters: `["type","object"]`},
		{name: "no object type", parameters: `{"type":"string"}`},
		{name: "unsupported structure", parameters: `{"type":"object","properties":["not","a","table"]}`},
		{name: "unsupported nested structure", parameters: `{"type":"object","properties":{"target":{"type":"string","required":"nope"}}}`},
		{name: "omits the bundled encrypted property", parameters: `{"type":"object","properties":{"task_name":{"type":"string"}}}`},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			parameters := testCase.parameters
			registry := registerV2WithOverrides(t, map[string]MultiAgentToolOverride{
				"spawn_agent": {Parameters: &parameters},
			})
			spawn, ok := registry.Lookup(tool.NamespacedName(MultiAgentV2Namespace, "spawn_agent"))
			if !ok {
				t.Fatal("spawn_agent was not registered")
			}
			properties, _ := spawn.Spec().InputSchema["properties"].(map[string]any)
			if _, ok := properties["fork_turns"]; !ok {
				t.Fatalf("bundled parameters were not retained: %#v", spawn.Spec().InputSchema)
			}
		})
	}
	// A description-only override never touches the parameters.
	description := "Catalog-only description."
	registry := registerV2WithOverrides(t, map[string]MultiAgentToolOverride{
		"list_agents": {Description: &description},
	})
	list, ok := registry.Lookup(tool.NamespacedName(MultiAgentV2Namespace, "list_agents"))
	if !ok {
		t.Fatal("list_agents was not registered")
	}
	spec := list.Spec()
	if spec.Description != description {
		t.Fatalf("description = %q", spec.Description)
	}
	properties, _ := spec.InputSchema["properties"].(map[string]any)
	if _, ok := properties["path_prefix"]; !ok {
		t.Fatalf("bundled parameters changed: %#v", spec.InputSchema)
	}
}

// TestMultiAgentV2UnknownCatalogEntriesAreIgnoredLikeRust covers the name
// selection: only the six V2 tools resolve catalog entries, and an unknown name
// is dropped by the registry lookup.
func TestMultiAgentV2UnknownCatalogEntriesAreIgnoredLikeRust(t *testing.T) {
	description := "Not a V2 tool."
	registry := registerV2WithOverrides(t, map[string]MultiAgentToolOverride{
		"unknown_tool": {Description: &description},
	})
	if _, ok := registry.Lookup(tool.NamespacedName(MultiAgentV2Namespace, "unknown_tool")); ok {
		t.Fatal("an unknown tool was registered")
	}
	if len(MultiAgentV2ToolNames) != 6 {
		t.Fatalf("MultiAgentV2ToolNames = %#v", MultiAgentV2ToolNames)
	}
	// The override map is only consulted per registered tool, so an unrelated
	// key cannot change the six bundled specs.
	for _, name := range MultiAgentV2ToolNames {
		executor, ok := registry.Lookup(tool.NamespacedName(MultiAgentV2Namespace, name))
		if !ok {
			t.Fatalf("%s was not registered", name)
		}
		if strings.Contains(executor.Spec().Description, description) {
			t.Fatalf("%s picked up an unrelated override", name)
		}
	}
	if _, err := json.Marshal(registry); err != nil {
		t.Fatalf("registry marshal error = %v", err)
	}
}
