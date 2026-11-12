package turn

import (
	"testing"

	"codex_go/model"
	"codex_go/tool"
)

// TestExecOutputNestedToolNamesLikeRust pins Rust #60914: Guardian may read the
// recorded inventory of a completed exec output, but only for the output that
// owns the cell and only once the inventory is final.
func TestExecOutputNestedToolNamesLikeRust(t *testing.T) {
	recorder := NewExecutedToolCallRecorder()
	recorder.RecordToolCall(&tool.Invocation{
		CallID: "exec-call", ToolName: tool.PlainName(tool.CodeModeExecToolName),
		Payload: tool.Payload{Kind: tool.PayloadCustom, Input: `text("running")`},
	}, model.ToolModeCodeMode)
	recorder.RegisterCell("cell-1", "exec-call")
	recorder.RecordToolCall(codeModeNestedInvocation("nested-1", "cell-1", "first"), model.ToolModeCodeMode)

	output := &ToolResponseItem{Type: "custom_tool_call_output", CallID: "exec-call", Output: NewFunctionCallOutputPayload("running", nil)}
	if _, ok := recorder.ExecOutputNestedToolNames("exec-call"); ok {
		t.Fatal("open cell reported a final inventory")
	}
	recorder.FinishCell("cell-1")
	input := &ToolResponseItem{Type: "custom_tool_call", CallID: "exec-call", Name: tool.CodeModeExecToolName}
	attached, token := recorder.AttachPendingToPrompt([]any{input, output})
	if attached == nil {
		t.Fatal("AttachPendingToPrompt() = nil")
	}
	recorder.CommitAttachment(token)

	names, ok := recorder.ExecOutputNestedToolNames("exec-call")
	if !ok {
		t.Fatal("completed exec output reported no inventory")
	}
	if len(names) != 1 || names[0] != "mcp__echo" {
		t.Fatalf("nested tool names = %#v", names)
	}
	// A wait output shares the cell but does not own the recorded inventory.
	if _, ok := recorder.ExecOutputNestedToolNames("wait-call"); ok {
		t.Fatal("another output claimed the completed inventory")
	}
	if _, ok := recorder.ExecOutputNestedToolNames(""); ok {
		t.Fatal("empty output id reported an inventory")
	}
}
