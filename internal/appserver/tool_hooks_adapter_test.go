package appserver

import (
	"context"
	"testing"

	"codex_go/internal/tool"
)

func TestToolHookAdapterRunsPostToolUseHook(t *testing.T) {
	command := hookRunnerOutputCommand(`{"decision":"block","reason":"review output"}`, "")
	hook := hookRunnerMetadata("post", HookEventPostToolUse, "echo", 0)
	hook.Command = &command
	adapter := NewToolHookAdapter(NewHookRunner(), []HookMetadata{hook}, "thread-1", "turn-1", t.TempDir())
	adapter.Model = "gpt-test"

	outcome, err := adapter.RunPostToolUse(context.Background(), &tool.Invocation{
		CallID:   "call-1",
		ToolName: tool.PlainName("echo"),
	}, &tool.PostToolUsePayload{
		ToolName:     &tool.HookToolName{Name: "echo"},
		ToolUseID:    "call-1",
		ToolInput:    map[string]any{},
		ToolResponse: "original",
	})
	if err != nil {
		t.Fatalf("RunPostToolUse() error = %v", err)
	}
	if outcome == nil || !outcome.Blocked || outcome.FeedbackMessage != "review output" {
		t.Fatalf("outcome = %+v", outcome)
	}
}
