package turn

import (
	"context"
	"testing"

	"codex_go/tool"
)

// Rust's hook runtime records every admitted turn input before it reaches a
// sampling request (`core/src/hook_runtime.rs`), including mail delivered to a
// running turn as `TurnInput::InterAgentCommunication`. The loop hands the
// drained steer items to the host at that same admission point so the host can
// persist them.
func TestAgentLoopReportsDrainedSteerInputItemsLikeRust(t *testing.T) {
	mailbox := NewSteerMailbox()
	if err := mailbox.Enqueue(&SteerEnqueueParams{
		ThreadID: "thread-steer-record",
		TurnID:   "turn-steer-record",
		InputItems: []any{map[string]any{
			"type":      "agent_message",
			"author":    "/root/worker",
			"recipient": "/root",
			"content":   []any{map[string]any{"type": "input_text", "text": "late mail"}},
		}},
	}); err != nil {
		t.Fatalf("Enqueue() error = %v", err)
	}
	agent := &fakeLoopAgent{}
	registry := tool.NewRegistry()
	if err := registry.Register(tool.NewExecutorFunc(tool.Spec{Name: tool.PlainName("echo")}, func(ctx context.Context, invocation *tool.Invocation) (*tool.Output, error) {
		return &tool.Output{Success: true, Body: "tool result"}, nil
	})); err != nil {
		t.Fatalf("register echo: %v", err)
	}
	loop := NewAgentLoop(&AgentLoopOptions{
		Agent:        agent,
		Dispatcher:   NewToolDispatcher(&ToolDispatcherOptions{Router: tool.NewRouter(registry)}),
		SteerMailbox: mailbox,
		MaxTurns:     3,
	})

	var recorded []any
	if _, err := loop.Run(context.Background(), &AgentLoopRequest{
		Prompt:       "original prompt",
		ThreadID:     "thread-steer-record",
		TurnID:       "turn-steer-record",
		SteerMailbox: mailbox,
		OnSteerInputItems: func(items []any) {
			recorded = append(recorded, items...)
		},
	}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	if len(recorded) != 1 {
		t.Fatalf("recorded steer items = %#v, want the one admitted communication", recorded)
	}
	item, ok := recorded[0].(map[string]any)
	if !ok || item["type"] != "agent_message" || item["author"] != "/root/worker" {
		t.Fatalf("recorded steer item = %#v, want the delivered agent_message", recorded[0])
	}
}
