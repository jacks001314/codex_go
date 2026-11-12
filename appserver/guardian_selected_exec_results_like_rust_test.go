package appserver

import (
	"testing"

	"codex_go/model"
	"codex_go/session"
	"codex_go/state"
	"codex_go/tool"
	"codex_go/turn"
)

// TestMarkGuardianSelectedExecResultsLikeRust pins Rust #60914: transcript
// entries whose recorded nested calls match the configured selection keep their
// own reservation, in either the recorded or normalized Code Mode spelling.
func TestMarkGuardianSelectedExecResultsLikeRust(t *testing.T) {
	entries := []state.GuardianTranscriptEntry{
		{Author: "tool", Label: "code", LineRole: "tool code result", Optional: true, Text: "selected", ExecNestedTools: []string{"mcp__echo__search"}},
		{Author: "tool", Label: "code", LineRole: "tool code result", Optional: true, Text: "normalized", ExecNestedTools: []string{"mcp__my-server__search"}},
		{Author: "tool", Label: "shell", LineRole: "tool shell result", Optional: true, Text: "ordinary", ExecNestedTools: []string{"shell"}},
		{Author: "user", LineRole: "user", Text: "instruction", ExecNestedTools: []string{"mcp__echo__search"}},
	}
	markGuardianSelectedExecResults(entries, []string{"mcp__echo__search", "mcp__my_server__search"})
	if !entries[0].SelectedExecResult {
		t.Fatal("recorded nested tool name was not selected")
	}
	if !entries[1].SelectedExecResult {
		t.Fatal("normalized nested tool name was not selected")
	}
	if entries[2].SelectedExecResult {
		t.Fatal("unrelated tool output was selected")
	}
	if !entries[3].SelectedExecResult {
		t.Fatal("matching evidence was not flagged")
	}
	if retention := entries[3].Retention(); retention.Optional {
		t.Fatalf("required user evidence became optional: %#v", retention)
	}
	if retention := entries[1].Retention(); retention.Priority != state.GuardianElasticPrioritySelectedExecResult {
		t.Fatalf("selected exec retention = %#v", retention)
	}

	// An empty selection leaves every entry alone.
	markGuardianSelectedExecResults(entries, nil)
	if !entries[0].SelectedExecResult {
		t.Fatal("clearing the selection mutated recorded evidence")
	}
}

// TestGuardianExecOutputNestedToolsReadsRecorderLikeRust covers the collection
// side: only a tool output with a final recorded inventory contributes nested
// tool names to the transcript entry.
func TestGuardianExecOutputNestedToolsReadsRecorderLikeRust(t *testing.T) {
	router := NewRuntimeRouter(RuntimeServices{})
	recorder := router.executedToolCallRecorder("thread-selected")
	recorder.RecordToolCall(&tool.Invocation{
		CallID: "exec-call", ToolName: tool.PlainName(tool.CodeModeExecToolName),
		Payload: tool.Payload{Kind: tool.PayloadCustom, Input: `text("running")`},
	}, model.ToolModeCodeMode)
	recorder.RegisterCell("cell-1", "exec-call")
	recorder.RecordToolCall(&tool.Invocation{
		CallID:   "nested-1",
		ToolName: tool.NamespacedName("mcp", "echo"),
		Payload:  tool.Payload{Kind: tool.PayloadFunction, Arguments: `{"message":"first"}`},
		Source:   "code_mode",
		Context:  map[string]any{tool.CodeModeCellIDContextKey: "cell-1"},
	}, model.ToolModeCodeMode)
	recorder.FinishCell("cell-1")
	input := &turn.ToolResponseItem{Type: "custom_tool_call", CallID: "exec-call", Name: tool.CodeModeExecToolName}
	output := &turn.ToolResponseItem{Type: "custom_tool_call_output", CallID: "exec-call", Output: turn.NewFunctionCallOutputPayload("running", nil)}
	_, token := recorder.AttachPendingToPrompt([]any{input, output})
	recorder.CommitAttachment(token)

	nested := router.guardianExecOutputNestedTools("thread-selected", &session.Item{Type: "tool_output", CallID: "exec-call"})
	if len(nested) != 1 || nested[0] != "mcp__echo" {
		t.Fatalf("nested tool names = %#v", nested)
	}
	if nested := router.guardianExecOutputNestedTools("thread-selected", &session.Item{Type: "function_call", CallID: "exec-call"}); nested != nil {
		t.Fatalf("function call claimed nested inventory: %#v", nested)
	}
	if nested := router.guardianExecOutputNestedTools("thread-selected", &session.Item{Type: "tool_output", CallID: "other"}); nested != nil {
		t.Fatalf("unknown output claimed nested inventory: %#v", nested)
	}
}
