package appserver

import (
	"testing"

	"codex_go/config"
	"codex_go/turn"
)

func mcpEnvironmentRuntimeConfigForTest() *config.Config {
	return &config.Config{Values: map[string]any{"mcp_servers": map[string]any{
		"local-server": map[string]any{"url": "https://local.example/mcp", "enabled": true},
		"env-a-server": map[string]any{"url": "https://a.example/mcp", "enabled": true, "environment_id": "env-a"},
		"env-b-server": map[string]any{"url": "https://b.example/mcp", "enabled": true, "environment_id": "env-b"},
	}}}
}

// TestMCPRuntimeTracksActiveTurnEnvironmentSelectionsLikeRust mirrors Rust
// #46335: MCP tool availability must follow the captured turn-environment
// snapshot, so a selection change saved for the next turn only takes effect on
// that turn (the runtime is refreshed when the captured selections change).
func TestMCPRuntimeTracksActiveTurnEnvironmentSelectionsLikeRust(t *testing.T) {
	router := NewRuntimeRouter(RuntimeServices{Config: config.NewConfigService(t.TempDir())})
	defer router.Close()
	cfg := mcpEnvironmentRuntimeConfigForTest()

	first := &turn.TurnStartParams{
		ThreadID:     "thread-envs",
		Environments: []map[string]any{{"environmentId": "env-a"}},
	}
	if err := router.registerActiveRuntimeTurn(first.ThreadID, "turn-1", nil, 1, first); err != nil {
		t.Fatalf("register first turn: %v", err)
	}
	service := router.mcpServiceForThread(first.ThreadID, cfg)
	if _, ok := service.ServerConfigForServer("env-a-server"); !ok {
		t.Fatal("selected environment server should be available on the first turn")
	}
	if _, ok := service.ServerConfigForServer("env-b-server"); ok {
		t.Fatal("unselected environment server should be disabled on the first turn")
	}
	// A selection saved while the turn runs (mutating the caller's params after
	// registration) must not change the active turn's availability.
	first.Environments = []map[string]any{{"environmentId": "env-b"}}
	service = router.mcpServiceForThread(first.ThreadID, cfg)
	if _, ok := service.ServerConfigForServer("env-a-server"); !ok {
		t.Fatal("the active turn snapshot must not follow a changed selection")
	}
	if _, ok := service.ServerConfigForServer("env-b-server"); ok {
		t.Fatal("a changed selection must not reach the running turn")
	}
	if _, ok := router.threads.ConsumeTurn(first.ThreadID, "turn-1", false); !ok {
		t.Fatal("first turn was not active")
	}

	// The next turn selects a different environment. The runtime must be
	// refreshed so the attachment-scoped servers follow the new snapshot.
	next := &turn.TurnStartParams{
		ThreadID:     "thread-envs",
		Environments: []map[string]any{{"environmentId": "env-b"}},
	}
	if err := router.registerActiveRuntimeTurn(next.ThreadID, "turn-2", nil, 2, next); err != nil {
		t.Fatalf("register second turn: %v", err)
	}
	service = router.mcpServiceForThread(next.ThreadID, cfg)
	if _, ok := service.ServerConfigForServer("env-b-server"); !ok {
		t.Fatal("newly selected environment server should be available on the next turn")
	}
	if _, ok := service.ServerConfigForServer("env-a-server"); ok {
		t.Fatal("deselected environment server should be disabled on the next turn")
	}
	if _, ok := service.ServerConfigForServer("local-server"); !ok {
		t.Fatal("local server should stay available across turns")
	}
}
