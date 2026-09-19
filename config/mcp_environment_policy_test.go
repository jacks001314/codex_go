package config

import (
	"reflect"
	"testing"
)

// TestEnvironmentMCPPolicyParsesAndRoundTripsLikeRust covers Rust's
// protocol::EnvironmentMcpPolicy shape: `servers` and `plugins`, with a nil map
// distinguishing "no allowlist" from an explicitly empty allowlist, and
// ToMap restoring the exact table form the app-server stores and re-parses.
func TestEnvironmentMCPPolicyParsesAndRoundTripsLikeRust(t *testing.T) {
	policy, err := EnvironmentMCPPolicyFromMap(map[string]any{
		"servers": map[string]any{
			"allowed": map[string]any{"identity": map[string]any{"url": "https://allowed.example/mcp"}},
			"matcher": map[string]any{"identity": map[string]any{"command": map[string]any{
				"executable": "npx",
				"args": []any{
					map[string]any{"match": "prefix", "value": "@demo/"},
					map[string]any{"match": "regex", "expression": "mcp-.*"},
				},
			}}},
		},
		"plugins": map[string]any{
			"demo":          map[string]any{"mcp_servers": map[string]any{}},
			"metadata-only": map[string]any{},
		},
	})
	if err != nil {
		t.Fatalf("EnvironmentMCPPolicyFromMap() error = %v", err)
	}
	if policy == nil || len(policy.MCPServers) != 2 || policy.Plugins["demo"].MCPServers == nil || *policy.Plugins["demo"].MCPServers == nil {
		t.Fatalf("policy = %#v", policy)
	}
	if policy.Plugins["metadata-only"].MCPServers != nil {
		t.Fatalf("metadata-only plugin = %#v, want no allowlist", policy.Plugins["metadata-only"])
	}
	if !policy.MCPServers["allowed"].Matches("", nil, "https://allowed.example/mcp") {
		t.Fatalf("allowed requirement did not match its identity: %#v", policy.MCPServers["allowed"])
	}
	if !policy.MCPServers["matcher"].Matches("npx", []string{"@demo/pkg", "mcp-server"}, "") {
		t.Fatalf("matcher requirement did not match: %#v", policy.MCPServers["matcher"])
	}

	roundTripped, err := EnvironmentMCPPolicyFromMap(policy.ToMap())
	if err != nil {
		t.Fatalf("round-trip parse error = %v", err)
	}
	if !reflect.DeepEqual(roundTripped, policy) {
		t.Fatalf("round-tripped policy = %#v, want %#v", roundTripped, policy)
	}
}

func TestEnvironmentMCPPolicyAbsentAndEmptyLikeRust(t *testing.T) {
	if policy, err := EnvironmentMCPPolicyFromMap(map[string]any{}); err != nil || policy != nil {
		t.Fatalf("empty table = %#v err=%v, want a nil policy", policy, err)
	}
	if policy, err := EnvironmentMCPPolicyFromMap(map[string]any{"servers": nil, "plugins": nil}); err != nil || policy != nil {
		t.Fatalf("null fields = %#v err=%v, want a nil policy", policy, err)
	}
	policy, err := EnvironmentMCPPolicyFromMap(map[string]any{"servers": map[string]any{}})
	if err != nil || policy == nil || policy.MCPServers == nil || len(policy.MCPServers) != 0 {
		t.Fatalf("empty allowlist = %#v err=%v, want a deny-all policy", policy, err)
	}
	if _, err := EnvironmentMCPPolicyFromMap(map[string]any{"servers": "nope"}); err == nil {
		t.Fatal("a non-table servers value must be rejected")
	}
	if _, err := EnvironmentMCPPolicyFromMap(map[string]any{
		"servers": map[string]any{"bad": map[string]any{"identity": map[string]any{}}},
	}); err == nil {
		t.Fatal("an identity without command or url must be rejected")
	}
}
