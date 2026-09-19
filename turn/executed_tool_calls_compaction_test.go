package turn

import (
	"testing"

	"codex_go/model"
	"codex_go/tool"
)

// TestExecutedToolCallRecorderAttachesCodeModeMetadataToCompactionPromptLikeRust
// mirrors Rust #46044 attach_to_compaction_prompt: compaction prompts carry the
// pending Code Mode inventory, and with capture disabled direct records already
// present in history are removed instead.
func TestExecutedToolCallRecorderAttachesCodeModeMetadataToCompactionPromptLikeRust(t *testing.T) {
	recorder := NewExecutedToolCallRecorder()
	recorder.RecordToolCall(&tool.Invocation{
		CallID: "exec-call", ToolName: tool.PlainName(tool.CodeModeExecToolName),
		Payload: tool.Payload{Kind: tool.PayloadCustom, Input: `text("running")`},
	}, model.ToolModeCodeMode)
	recorder.RecordToolCall(codeModeNestedInvocation("nested-1", "cell-1", "first"), model.ToolModeCodeMode)
	recorder.RegisterCell("cell-1", "exec-call")

	execOutput := &ToolResponseItem{Type: "custom_tool_call_output", CallID: "exec-call", Output: NewFunctionCallOutputPayload("running", nil)}
	attached := recorder.AttachToCompactionPrompt([]any{execOutput}, true)
	calls := executedToolCallsFromObject(t, marshalExecutedToolCallItem(t, model.BoundExecutedToolCallsForPrompt(attached)[0]))
	if len(calls) != 1 || calls[0]["name"] != "mcp__echo" {
		t.Fatalf("compaction prompt calls = %#v", calls)
	}
	// The compaction attach does not consume the pending observation, so a later
	// normal sampling request still attaches it.
	normal, token := recorder.AttachPendingToPrompt([]any{execOutput})
	normalCalls := executedToolCallsFromObject(t, marshalExecutedToolCallItem(t, model.BoundExecutedToolCallsForPrompt(normal)[0]))
	if len(normalCalls) != 1 || normalCalls[0]["name"] != "mcp__echo" {
		t.Fatalf("sampling prompt calls after compaction = %#v", normalCalls)
	}
	recorder.CommitAttachment(token)

	directOutput := &ToolResponseItem{Type: "function_call_output", CallID: "direct", Output: NewFunctionCallOutputPayload("ok", nil)}
	directOutput.SetExecutedToolCallsComplete(true)
	directOutput.ReplaceExecutedToolCalls([]model.ExecutedToolCall{model.NewExecutedToolCall("echo", map[string]any{"text": "hi"})})
	cellOutput := &ToolResponseItem{Type: "custom_tool_call_output", CallID: "cell-output", Output: NewFunctionCallOutputPayload("ok", nil)}
	cellOutput.SetExecutedToolCallCell("cell-1")
	cellOutput.ReplaceExecutedToolCalls([]model.ExecutedToolCall{model.NewExecutedToolCall("nested", map[string]any{})})

	items := recorder.AttachToCompactionPrompt([]any{directOutput, cellOutput}, false)
	if got := len(items[0].(*ToolResponseItem).ExecutedToolCalls()); got != 0 {
		t.Fatalf("disabled capture kept direct metadata: %d calls", got)
	}
	if got := len(items[1].(*ToolResponseItem).ExecutedToolCalls()); got != 1 {
		t.Fatalf("disabled capture stripped Code Mode metadata: %d calls", got)
	}
}
