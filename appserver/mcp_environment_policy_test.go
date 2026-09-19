package appserver

import (
	"testing"

	"codex_go/config"
	"codex_go/turn"
)

// TestOwnerSuppliedEnvironmentMCPPolicyRestrictsServersLikeRust mirrors Rust
// #39335: an owner-supplied environment mcp_policy restricts which configured
// MCP servers are available in that environment, a pending owner selection keeps
// them unavailable, and a thread-supplied selection stays unrestricted.
func TestOwnerSuppliedEnvironmentMCPPolicyRestrictsServersLikeRust(t *testing.T) {
	router := NewRuntimeRouter(RuntimeServices{Config: config.NewConfigService(t.TempDir())})
	defer router.Close()
	cfg := &config.Config{Values: map[string]any{"mcp_servers": map[string]any{
		"allowed-server": map[string]any{"url": "https://allowed.example/mcp", "enabled": true, "environment_id": "env-a"},
		"other-server":   map[string]any{"url": "https://other.example/mcp", "enabled": true, "environment_id": "env-a"},
		"local-server":   map[string]any{"url": "https://local.example/mcp", "enabled": true},
	}}}
	threadID := "thread-owner-policy"

	assertServers := func(t *testing.T, step string, allowed, other bool) {
		t.Helper()
		service := router.mcpServiceForThread(threadID, cfg)
		if _, ok := service.ServerConfigForServer("allowed-server"); ok != allowed {
			t.Fatalf("%s: allowed-server available = %v, want %v", step, ok, allowed)
		}
		if _, ok := service.ServerConfigForServer("other-server"); ok != other {
			t.Fatalf("%s: other-server available = %v, want %v", step, ok, other)
		}
		if _, ok := service.ServerConfigForServer("local-server"); !ok {
			t.Fatalf("%s: the controller-owned local server must stay available", step)
		}
	}

	register := func(t *testing.T, turnID string, selection map[string]any) {
		t.Helper()
		normalized, err := validateEnvironmentSelectionConfig(selectionEnvironmentID(selection), selection)
		if err != nil {
			t.Fatalf("validateEnvironmentSelectionConfig() error = %v", err)
		}
		// Mirrors the normalization validateTurnEnvironmentSelections applies.
		if normalized == nil {
			delete(selection, "config")
		} else {
			selection["config"] = normalized
		}
		params := &turn.TurnStartParams{ThreadID: threadID, Environments: []map[string]any{selection}}
		if err := router.registerActiveRuntimeTurn(threadID, turnID, nil, 1, params); err != nil {
			t.Fatalf("register %s: %v", turnID, err)
		}
	}

	// A ready owner config with an allowlist admits only the listed server.
	register(t, "turn-ready", map[string]any{
		"environmentId": "env-a",
		"config": map[string]any{"state": "ready", "config": map[string]any{
			"mcp_policy": map[string]any{"servers": map[string]any{
				"allowed-server": map[string]any{"identity": map[string]any{"url": "https://allowed.example/mcp"}},
			}},
		}},
	})
	assertServers(t, "owner allowlist", true, false)
	router.threads.ConsumeTurn(threadID, "turn-ready", false)

	// A pending owner selection cannot claim owner authority, so its servers are
	// unavailable until configuration arrives.
	register(t, "turn-pending", map[string]any{
		"environmentId": "env-a",
		"config":        map[string]any{"state": "pending"},
	})
	assertServers(t, "pending owner config", false, false)
	router.threads.ConsumeTurn(threadID, "turn-pending", false)

	// A failed owner selection behaves the same way.
	register(t, "turn-failed", map[string]any{
		"environmentId": "env-a",
		"config":        map[string]any{"state": "failed", "error": "provisioning failed"},
	})
	assertServers(t, "failed owner config", false, false)
	router.threads.ConsumeTurn(threadID, "turn-failed", false)

	// A thread-supplied selection is unrestricted.
	register(t, "turn-thread", map[string]any{"environmentId": "env-a"})
	assertServers(t, "thread-supplied config", true, true)
	router.threads.ConsumeTurn(threadID, "turn-thread", false)

	// The policy is re-read from the stored selection on the next turn.
	register(t, "turn-ready-again", map[string]any{
		"environmentId": "env-a",
		"config": map[string]any{"state": "ready", "config": map[string]any{
			"mcp_policy": map[string]any{"servers": map[string]any{}},
		}},
	})
	assertServers(t, "owner deny-all", false, false)
}
