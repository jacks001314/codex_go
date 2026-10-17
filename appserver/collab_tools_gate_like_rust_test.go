package appserver

import (
	"testing"

	"codex_go/agent"
	"codex_go/config"
)

// TestCollabToolsGateMatchesRustLikeRust covers Rust's `collab_tools_enabled`
// after #53617: `features.multi_agent_v2.disable_native_tools` hides the
// native collaboration tools for every V2 thread (root included) while the V2
// runtime and its instructions stay enabled for host-provided tools.
func TestCollabToolsGateMatchesRustLikeRust(t *testing.T) {
	cases := []struct {
		name               string
		version            agent.MultiAgentVersion
		depthLimited       bool
		disableNativeTools bool
		subagentTools      bool
		want               bool
	}{
		{name: "v1 root", version: agent.VersionV1, want: true},
		{name: "v1 depth limited", version: agent.VersionV1, depthLimited: true, want: false},
		{name: "v2 root", version: agent.VersionV2, subagentTools: true, want: true},
		{name: "v2 root native tools disabled", version: agent.VersionV2, disableNativeTools: true, subagentTools: true, want: false},
		{name: "v2 subagent without v2 model", version: agent.VersionV2, subagentTools: false, want: false},
		{name: "v2 subagent with v2 model", version: agent.VersionV2, subagentTools: true, want: true},
		{name: "v2 subagent native tools disabled", version: agent.VersionV2, disableNativeTools: true, subagentTools: true, want: false},
		{name: "v2 ignores the depth limit", version: agent.VersionV2, depthLimited: true, subagentTools: true, want: true},
		{name: "unset version", version: "", want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := runtimeCollabToolsEnabled(tc.version, tc.depthLimited, tc.disableNativeTools, tc.subagentTools); got != tc.want {
				t.Fatalf("runtimeCollabToolsEnabled(%q, %v, %v, %v) = %v, want %v", tc.version, tc.depthLimited, tc.disableNativeTools, tc.subagentTools, got, tc.want)
			}
		})
	}
}

// TestDisableNativeToolsKeepsV2RuntimeLikeRust checks the configuration half:
// the option is parsed from `features.multi_agent_v2.disable_native_tools`
// without disabling the surrounding V2 runtime settings.
func TestDisableNativeToolsKeepsV2RuntimeLikeRust(t *testing.T) {
	cfg := &config.Config{Values: map[string]any{"features": map[string]any{"multi_agent_v2": map[string]any{
		"disable_native_tools": true,
		"wait_agent_enabled":   true,
	}}}}
	v2, err := cfg.MultiAgentV2Config(0)
	if err != nil {
		t.Fatalf("MultiAgentV2Config() error = %v", err)
	}
	if !v2.DisableNativeTools || !v2.WaitAgentEnabled {
		t.Fatalf("v2 config = %#v, want disable_native_tools and wait_agent_enabled", v2)
	}
}
