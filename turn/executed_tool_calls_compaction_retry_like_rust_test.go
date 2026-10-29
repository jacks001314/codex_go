package turn

import (
	"testing"

	"codex_go/model"
	"codex_go/tool"
)

// Mirrors Rust #46044: a compaction prompt keeps captured Direct records that
// already belong to their output instead of rebuilding or stripping them.
func TestCompactionPromptPreservesCapturedDirectRecordsLikeRust(t *testing.T) {
	recorder := NewExecutedToolCallRecorder()
	// Giving the recorder a live lifetime is what "metadata enabled" means; a
	// direct record captured under it must survive compaction untouched.
	recorder.RecordToolCall(codeModeNestedInvocation("nested-1", "cell-1", "first"), model.ToolModeCodeMode)
	recorder.RegisterCell("cell-1", "exec-call")
	recorder.FinishCell("cell-1")

	directOutput := &ToolResponseItem{ID: "fco_direct", Type: "function_call_output", CallID: "direct", Output: NewFunctionCallOutputPayload("ok", nil)}
	directOutput.SetExecutedToolCallsComplete(true)
	directOutput.ReplaceExecutedToolCalls([]model.ExecutedToolCall{model.NewExecutedToolCall("echo", map[string]any{"text": "hi"})})

	items := recorder.AttachToCompactionPrompt([]any{directOutput}, true)
	calls := attachedCallsForItem(t, items[0])
	if len(calls) != 1 || calls[0]["name"] != "echo" {
		t.Fatalf("compaction prompt direct calls = %#v", calls)
	}
	metadata, ok := marshalExecutedToolCallItem(t, model.BoundExecutedToolCallsForPrompt([]any{items[0]})[0])["internal_chat_message_metadata_passthrough"].(map[string]any)
	if !ok || metadata["tool_calls_complete"] != true {
		t.Fatalf("captured direct completeness = %#v", metadata)
	}
	if cellID, present := metadata["cell_id"]; present && cellID != nil {
		t.Fatalf("direct record gained a cell id: %#v", cellID)
	}
}

// Mirrors Rust #46044's retry retention: a shortened compaction attempt is not
// an installed history window, so observations it could not attach stay
// recorded and a later normal sampling request still emits them.
func TestCompactionRetryRetainsOmittedCodeModeObservationsLikeRust(t *testing.T) {
	recorder := NewExecutedToolCallRecorder()
	recorder.RecordToolCall(&tool.Invocation{
		CallID:   "exec-call",
		ToolName: tool.PlainName(tool.CodeModeExecToolName),
		Payload:  tool.Payload{Kind: tool.PayloadCustom, Input: `text("running")`},
	}, model.ToolModeCodeMode)
	recorder.RecordToolCall(codeModeNestedInvocation("nested-1", "cell-1", "first"), model.ToolModeCodeMode)
	recorder.RegisterCell("cell-1", "exec-call")
	recorder.FinishCell("cell-1")
	execInput := codeModeExecInputItem("exec-call")
	execOutput := &ToolResponseItem{Type: "custom_tool_call_output", CallID: "exec-call", Output: NewFunctionCallOutputPayload("running", nil)}

	// The compaction history is shortened past the exec output, so the pending
	// inventory cannot be attached to anything.
	shortened := []any{&model.AgentItem{ID: "msg-1", Type: "message"}}
	compact := recorder.AttachToCompactionPrompt(shortened, true)
	if attachedCallsOrNil(t, compact[0]) != nil {
		t.Fatalf("shortened compaction prompt grew metadata: %#v", compact[0])
	}

	// A normal sampling request over the live window still sees the observation.
	normal, token := recorder.AttachPendingToPrompt([]any{execInput, execOutput})
	calls := attachedCallsForItem(t, normal[1])
	if len(calls) != 1 || calls[0]["name"] != "mcp__echo" {
		t.Fatalf("sampling calls after a shortened compaction = %#v", calls)
	}
	recorder.CommitAttachment(token)

	// A second shortened compaction attempt must not drop the committed binding.
	recorder.AttachToCompactionPrompt(shortened, true)

	again, againToken := recorder.AttachPendingToPrompt([]any{execInput, execOutput})
	if againToken != nil {
		t.Fatalf("committed binding was consumed again: %#v", againToken)
	}
	againCalls := attachedCallsForItem(t, again[1])
	if len(againCalls) != 1 || againCalls[0]["name"] != "mcp__echo" {
		t.Fatalf("retained sampling calls after retries = %#v", againCalls)
	}
	metadata, ok := marshalExecutedToolCallItem(t, model.BoundExecutedToolCallsForPrompt([]any{again[1]})[0])["internal_chat_message_metadata_passthrough"].(map[string]any)
	if !ok || metadata["tool_calls_complete"] != true {
		t.Fatalf("retained completeness after retries = %#v", metadata)
	}
}
