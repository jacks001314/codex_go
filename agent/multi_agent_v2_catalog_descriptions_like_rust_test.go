package agent

import (
	"reflect"
	"strings"
	"testing"

	"codex_go/tool"
)

// TestMultiAgentV2CatalogDescriptionsCoverEveryToolLikeRust covers Rust
// #46297: `model_messages.tools.multi_agent` description overrides resolve for
// every Multi-Agent V2 tool, not just spawn_agent. A missing entry keeps the
// bundled text, an empty string suppresses the static text without disabling the
// tool, parameter schemas stay bundled, and the override follows the configured
// namespace.
func TestMultiAgentV2CatalogDescriptionsCoverEveryToolLikeRust(t *testing.T) {
	controller := &limitV2Controller{MemoryToolController: NewMemoryToolController()}
	newRegistry := func(overrides map[string]MultiAgentToolOverride, namespace string) *tool.Registry {
		t.Helper()
		registry := tool.NewRegistry()
		if err := RegisterMultiAgentHandlersWithOptions(registry, &MultiAgentHandlerOptions{
			Controller:    controller,
			Version:       VersionV2,
			Exposure:      tool.ExposureModelVisible,
			Namespace:     namespace,
			ToolOverrides: overrides,
		}); err != nil {
			t.Fatalf("RegisterMultiAgentHandlersWithOptions() error = %v", err)
		}
		return registry
	}

	baseline := newRegistry(nil, "")
	empty := ""
	overrides := map[string]MultiAgentToolOverride{}
	for _, name := range []string{"send_message", "followup_task", "wait_agent", "interrupt_agent"} {
		description := "Catalog " + name + " description."
		overrides[name] = MultiAgentToolOverride{Description: &description}
	}
	// An empty description suppresses the bundled static text for a tool that
	// has no runtime-composed guidance.
	overrides["list_agents"] = MultiAgentToolOverride{Description: &empty}
	overridden := newRegistry(overrides, "custom_agents")

	for _, name := range MultiAgentV2ToolNames {
		bundled, ok := baseline.Lookup(tool.NamespacedName(MultiAgentV2Namespace, name))
		if !ok {
			t.Fatalf("baseline %s was not registered", name)
		}
		current, ok := overridden.Lookup(tool.NamespacedName("custom_agents", name))
		if !ok {
			t.Fatalf("override %s was not registered under the configured namespace", name)
		}
		bundledSpec, currentSpec := bundled.Spec(), current.Spec()
		if !reflect.DeepEqual(currentSpec.InputSchema, bundledSpec.InputSchema) {
			t.Fatalf("%s input schema changed: %#v", name, currentSpec.InputSchema)
		}
		override, hasOverride := overrides[name]
		switch {
		case !hasOverride:
			if currentSpec.Description != bundledSpec.Description {
				t.Fatalf("%s description = %q without an override, want the bundled text", name, currentSpec.Description)
			}
		case override.Description != nil && *override.Description == "":
			if currentSpec.Description != "" {
				t.Fatalf("%s description = %q, want the static text suppressed", name, currentSpec.Description)
			}
		default:
			if currentSpec.Description != *override.Description {
				t.Fatalf("%s description = %q, want the catalog override", name, currentSpec.Description)
			}
		}
	}
}

// TestMultiAgentV2DescriptionOverrideKeepsSpawnRuntimeGuidanceLikeRust pins the
// spawn_agent half of Rust #46297: the catalog text replaces only the bundled
// static sentence, while the inherited-model guidance and the usage hint stay.
func TestMultiAgentV2DescriptionOverrideKeepsSpawnRuntimeGuidanceLikeRust(t *testing.T) {
	description := "Catalog spawn_agent description."
	hint := "Configured usage hint."
	registry := tool.NewRegistry()
	if err := RegisterMultiAgentHandlersWithOptions(registry, &MultiAgentHandlerOptions{
		Controller:                &limitV2Controller{MemoryToolController: NewMemoryToolController()},
		Version:                   VersionV2,
		Exposure:                  tool.ExposureModelVisible,
		UsageHintText:             &hint,
		ExposeSpawnModelOverrides: true,
		ToolOverrides: map[string]MultiAgentToolOverride{
			"spawn_agent": {Description: &description},
		},
	}); err != nil {
		t.Fatalf("RegisterMultiAgentHandlersWithOptions() error = %v", err)
	}
	spawn, ok := registry.Lookup(tool.NamespacedName(MultiAgentV2Namespace, "spawn_agent"))
	if !ok {
		t.Fatal("spawn_agent was not registered")
	}
	spec := spawn.Spec()
	if !reflect.DeepEqual(spec.InputSchema["required"], []any{"task_name", "message"}) {
		t.Fatalf("spawn_agent input schema changed: %#v", spec.InputSchema)
	}
	if !strings.HasPrefix(spec.Description, description) {
		t.Fatalf("spawn_agent description = %q, want the catalog override first", spec.Description)
	}
	if !strings.Contains(spec.Description, multiAgentV2SpawnInheritedModelGuidance) {
		t.Fatalf("spawn_agent description = %q, want the inherited-model guidance", spec.Description)
	}
	if !strings.HasSuffix(spec.Description, hint) {
		t.Fatalf("spawn_agent description = %q, want the usage hint last", spec.Description)
	}
}
