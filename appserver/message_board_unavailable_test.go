package appserver

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codex_go/agentboard"
	"codex_go/config"
	"codex_go/tool"
	"codex_go/turn"
)

// Rust #53369: disabling direct messaging while the message board is absent
// must not reject the turn. The board tools are simply not advertised.
func TestDisableDirectMessageAllowsUnavailableBoardLikeRust(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(config.ConfigPath(home), []byte(`
[features]
multi_agent = true

[features.multi_agent_v2]
enabled = true
disable_direct_message = true
`), 0o600); err != nil {
		t.Fatal(err)
	}
	router := NewRuntimeRouter(RuntimeServices{Config: config.NewConfigService(home)})
	toolRouter, err := router.toolRouterForTurn(t.TempDir(), &turn.TurnStartParams{
		ThreadID: "thread-board-unavailable",
	}, "turn-board-unavailable")
	if err != nil {
		t.Fatalf("toolRouterForTurn() error = %v", err)
	}
	for _, name := range []string{"post", "send_message", "followup_task"} {
		if _, ok := toolRouter.Executor(tool.ToolName{Namespace: "collaboration", Name: name}); ok {
			t.Fatalf("unavailable board tool %q was advertised", name)
		}
	}
	if _, err := toolRouter.Dispatch(context.Background(), &tool.Invocation{
		ToolName: tool.ToolName{Namespace: "collaboration", Name: "post"},
		Payload:  tool.Payload{Kind: tool.PayloadFunction, Arguments: `{}`},
	}); err == nil || !strings.Contains(err.Error(), "tool not found") {
		t.Fatalf("unavailable board call error = %v, want the model-visible unsupported-tool error", err)
	}
	if _, err := os.Stat(filepath.Join(home, agentboard.AgentMessageBoardDatabaseFile)); !os.IsNotExist(err) {
		t.Fatalf("board database exists without a board: %v", err)
	}
}
