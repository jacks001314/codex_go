package agent

import (
	"testing"

	"codex_go/tool"
)

// TestMultiAgentV2DisableDirectMessageOmitsDirectToolsLikeRust covers Rust
// #47540 `features.multi_agent_v2.disable_direct_message`: send_message and
// followup_task disappear while spawning, waits and management tools remain.
func TestMultiAgentV2DisableDirectMessageOmitsDirectToolsLikeRust(t *testing.T) {
	registry := tool.NewRegistry()
	if err := RegisterMultiAgentHandlersWithOptions(registry, &MultiAgentHandlerOptions{
		Controller: NewMemoryToolController(), Version: VersionV2, Namespace: MultiAgentV2Namespace,
		DisableDirectMessage: true,
	}); err != nil {
		t.Fatalf("RegisterMultiAgentHandlersWithOptions() error = %v", err)
	}
	for _, name := range []string{"send_message", "followup_task"} {
		if _, ok := registry.Lookup(tool.NamespacedName(MultiAgentV2Namespace, name)); ok {
			t.Fatalf("%s.%s is exposed with disable_direct_message", MultiAgentV2Namespace, name)
		}
	}
	for _, name := range []string{"spawn_agent", "wait_agent", "interrupt_agent", "list_agents"} {
		if _, ok := registry.Lookup(tool.NamespacedName(MultiAgentV2Namespace, name)); !ok {
			t.Fatalf("%s.%s must stay available with disable_direct_message", MultiAgentV2Namespace, name)
		}
	}
}

// TestMultiAgentV2DirectToolsDefaultOnLikeRust keeps the default surface
// unchanged when the option is absent.
func TestMultiAgentV2DirectToolsDefaultOnLikeRust(t *testing.T) {
	registry := tool.NewRegistry()
	if err := RegisterMultiAgentHandlersWithOptions(registry, &MultiAgentHandlerOptions{
		Controller: NewMemoryToolController(), Version: VersionV2, Namespace: MultiAgentV2Namespace,
	}); err != nil {
		t.Fatalf("RegisterMultiAgentHandlersWithOptions() error = %v", err)
	}
	for _, name := range []string{"send_message", "followup_task"} {
		if _, ok := registry.Lookup(tool.NamespacedName(MultiAgentV2Namespace, name)); !ok {
			t.Fatalf("%s.%s must be exposed by default", MultiAgentV2Namespace, name)
		}
	}
}
