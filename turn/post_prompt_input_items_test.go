package turn

import (
	"context"
	"testing"

	"codex_go/tool"
)

func TestAgentLoopPostPromptInputItemsFollowThePrompt(t *testing.T) {
	agent := &fakeLoopAgent{}
	registry := tool.NewRegistry()
	if err := registry.Register(tool.NewExecutorFunc(tool.Spec{Name: tool.PlainName("echo")}, func(ctx context.Context, invocation *tool.Invocation) (*tool.Output, error) {
		return &tool.Output{Success: true, Body: "tool result"}, nil
	})); err != nil {
		t.Fatalf("register echo: %v", err)
	}
	loop := NewAgentLoop(&AgentLoopOptions{
		Agent:      agent,
		Dispatcher: NewToolDispatcher(&ToolDispatcherOptions{Router: tool.NewRouter(registry)}),
		MaxTurns:   3,
	})
	update := map[string]any{"type": "configuration_update", "reasoning": map[string]any{"effort": "high"}}
	result, err := loop.Run(context.Background(), &AgentLoopRequest{
		Prompt:               "run echo",
		PostPromptInputItems: []any{update},
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(agent.requests) != 2 {
		t.Fatalf("agent requests = %#v", agent.requests)
	}
	if len(agent.requests[0].PostPromptInputItems) != 1 {
		t.Fatalf("first request post items = %#v", agent.requests[0].PostPromptInputItems)
	}
	if len(agent.requests[1].PostPromptInputItems) != 0 {
		t.Fatalf("later requests must not repeat post items: %#v", agent.requests[1].PostPromptInputItems)
	}
	if len(result.InputItems) < 2 {
		t.Fatalf("result input = %#v", result.InputItems)
	}
	prompt, ok := result.InputItems[0].(map[string]any)
	if !ok || prompt["type"] != "message" || prompt["role"] != "user" {
		t.Fatalf("result input[0] = %#v, want prompt user message", result.InputItems[0])
	}
	follow, ok := result.InputItems[1].(map[string]any)
	if !ok || follow["type"] != "configuration_update" {
		t.Fatalf("result input[1] = %#v, want configuration update after the prompt", result.InputItems[1])
	}
}
