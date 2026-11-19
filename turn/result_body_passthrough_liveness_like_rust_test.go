package turn

import (
	"context"
	"strconv"
	"testing"

	"codex_go/model"
	"codex_go/tool"
)

// Mirrors Rust #62392/#62423 recorder liveness: the connector passthrough feature
// alone keeps the recorder running, so a direct Apps call still retains its result
// body even when ordinary executed-tool metadata is disabled.
func TestRuntimeKeepsRecorderForConnectorPassthroughLikeRust(t *testing.T) {
	connectorOutput := func() *tool.Output {
		return &tool.Output{
			Success: true,
			Body:    "ok",
			ResultBodySource: &tool.ResultBodySource{
				Content: []any{map[string]any{"type": "text", "text": "connector body"}},
			},
		}
	}
	for _, passthrough := range []bool{false, true} {
		t.Run(strconv.FormatBool(passthrough), func(t *testing.T) {
			agent := &fakeLoopAgent{}
			registry := tool.NewRegistry()
			if err := registry.Register(tool.NewExecutorFunc(tool.Spec{Name: tool.PlainName("echo")}, func(context.Context, *tool.Invocation) (*tool.Output, error) {
				return connectorOutput(), nil
			})); err != nil {
				t.Fatalf("register echo: %v", err)
			}
			runtime := NewRuntime(&RuntimeOptions{Agent: agent, Router: tool.NewRouter(registry)})
			runtime.executedToolCalls.SetResultBodyPassthrough(passthrough)
			runtime.executedToolCalls.BeginResultBodyTurn("turn-1", "chatgpt|acct", true)

			if _, err := runtime.Run(context.Background(), &AgentLoopRequest{
				Prompt:                                "run",
				TurnID:                                "turn-1",
				ExecutedToolCallMetadataEnabled:       false,
				ConnectorResultBodyPassthroughEnabled: passthrough,
			}); err != nil {
				t.Fatalf("Run() error = %v", err)
			}
			var rows []model.ExecutedToolCall
			for _, input := range agent.requests[1].InputItems {
				if item, ok := input.(*ToolResponseItem); ok {
					rows = append(rows, item.ExecutedToolCalls()...)
				}
			}
			if !passthrough {
				if len(rows) != 0 {
					t.Fatalf("rows without passthrough = %d, want 0", len(rows))
				}
				return
			}
			if len(rows) != 1 || rows[0].ToolResultID() == "" {
				t.Fatalf("rows = %#v, want one row carrying a captured body", rows)
			}
			store := runtime.executedToolCalls.currentResultBodyTurn()
			if store == nil {
				t.Fatal("the turn lost its body store")
			}
			store.mu.Lock()
			retained := store.bytes
			store.mu.Unlock()
			if retained == 0 {
				t.Fatal("the passthrough turn retained no body bytes")
			}
		})
	}
}
