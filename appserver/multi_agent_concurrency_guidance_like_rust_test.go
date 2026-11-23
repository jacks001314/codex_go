package appserver

import (
	"strings"
	"testing"

	"codex_go/agent"
	"codex_go/config"
)

// Rust #62864: with multi_agent_v2.disable_native_tools set, the default role
// guidance keeps the concurrency slot count for bundled and catalog text,
// while explicit overrides still win and empty role text still suppresses it.
func TestDisabledNativeToolsKeepsConcurrencyGuidanceLikeRust(t *testing.T) {
	v2 := &config.MultiAgentV2Config{DisableNativeTools: true, MaxConcurrentThreadsPerSession: 17}
	catalog := "catalog role text"
	for _, tc := range []struct {
		name    string
		catalog *string
	}{
		{name: "bundled"},
		{name: "catalog", catalog: &catalog},
	} {
		t.Run(tc.name, func(t *testing.T) {
			role, ok := resolveMultiAgentRoleInstructions(nil, tc.catalog, agent.MultiAgentV2RootUsageHint, v2, false)
			if !ok {
				t.Fatal("disabled native tools dropped the default role guidance")
			}
			rendered := role.render()
			want := "There are 17 available concurrency slots, meaning that up to 17 agents can be active at once, including you."
			if !strings.Contains(rendered, want) {
				t.Fatalf("role = %q, want the concurrency guidance", rendered)
			}
			if strings.Contains(rendered, agent.MultiAgentV2SharedUsageHint) {
				t.Fatalf("role = %q, want only the concurrency guidance", rendered)
			}
			if !strings.HasPrefix(rendered, multiAgentRoleOpenMarker) || !strings.HasSuffix(rendered, multiAgentRoleCloseMarker) {
				t.Fatalf("role = %q, want the multi_agent_role markers", rendered)
			}
		})
	}

	override := "custom role"
	role, ok := resolveMultiAgentRoleInstructions(&override, nil, agent.MultiAgentV2RootUsageHint, v2, false)
	if want := multiAgentRoleOpenMarker + override + multiAgentRoleCloseMarker; !ok || role.render() != want {
		t.Fatalf("override role = %q (ok=%v), want %q", role.render(), ok, want)
	}
	empty := ""
	if _, ok := resolveMultiAgentRoleInstructions(&empty, nil, agent.MultiAgentV2RootUsageHint, v2, false); ok {
		t.Fatal("empty role text must still suppress the guidance")
	}
}
