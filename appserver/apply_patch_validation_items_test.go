package appserver

import (
	"testing"
	"time"

	"codex_go/tool"
	"codex_go/turn"
)

func TestApplyPatchValidationItemsRemainPersistedButHiddenFromThread(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	execution := &turn.ToolExecutionResult{
		Invocation: &tool.Invocation{
			CallID: "patch-validation", ToolName: tool.PlainName(tool.DefaultApplyPatchToolName),
			Payload: tool.Payload{Kind: tool.PayloadCustom, Input: "invalid patch"},
		},
		Output: &tool.Output{
			CallID: "patch-validation", ToolName: tool.PlainName(tool.DefaultApplyPatchToolName),
			Success: false, Body: "apply_patch verification failed: missing context",
			Error: "apply_patch verification failed: missing context", CompletedAt: now,
		},
		StartedAt:  now,
		FinishedAt: now,
	}

	call, ok := sessionItemForAppToolCall("turn-1", execution, now, nil)
	if !ok {
		t.Fatal("sessionItemForAppToolCall() did not preserve validation call")
	}
	output, ok := sessionItemForAppToolOutput("turn-1", execution, now, nil)
	if !ok {
		t.Fatal("sessionItemForAppToolOutput() did not preserve validation output")
	}
	if !sessionItemIsHiddenThreadItem(&call) || !sessionItemIsHiddenThreadItem(&output) {
		t.Fatalf("validation items must be hidden from app-server notifications: call=%#v output=%#v", call.Metadata, output.Metadata)
	}
	if call.Type != "custom_tool_call" || output.Type != "tool_output" {
		t.Fatalf("persisted item types = %q, %q", call.Type, output.Type)
	}
}
