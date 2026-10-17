package turn

import (
	"strings"
	"testing"

	"codex_go/model"
	"codex_go/tool"
)

func completeCellMetadata(t *testing.T, item any) map[string]any {
	t.Helper()
	object := marshalExecutedToolCallItem(t, item)
	metadata, ok := object["internal_chat_message_metadata_passthrough"].(map[string]any)
	if !ok {
		t.Fatalf("metadata = %#v", object["internal_chat_message_metadata_passthrough"])
	}
	return metadata
}

func TestExecutedToolCallRecorderPreservesLargeArgumentsAndCompletenessLikeRust(t *testing.T) {
	recorder := NewExecutedToolCallRecorder()
	recorder.StartCell("large-cell", "exec")
	arguments := `{"padding":"` + strings.Repeat("x", 40*1024) + `"}`
	recorder.RecordToolCall(&tool.Invocation{
		CallID:   "nested",
		ToolName: tool.NamespacedName("mcp", "echo"),
		Payload:  tool.Payload{Kind: tool.PayloadFunction, Arguments: arguments},
		Source:   "code_mode",
		Context:  map[string]any{tool.CodeModeCellIDContextKey: "large-cell"},
	}, model.ToolModeCodeMode)
	recorder.FinishCell("large-cell")

	history := []any{codeModeExecInputItem("exec"), codeModeExecOutputItem("exec")}
	attached, token := recorder.AttachPendingToPrompt(history)
	if token == nil {
		t.Fatal("large nested inventory did not attach")
	}
	recorder.CommitAttachment(token)
	calls := attachedCallsForItem(t, attached[1])
	if len(calls) != 1 {
		t.Fatalf("attached calls = %#v, want one call", calls)
	}
	if _, truncated := calls[0]["arguments"].(map[string]any)["_codex_executed_tool_call_truncated"]; truncated {
		t.Fatalf("large arguments were truncated by the recorder: %#v", calls[0])
	}
	if got := calls[0]["arguments"].(map[string]any)["padding"]; got != strings.Repeat("x", 40*1024) {
		t.Fatalf("recorded argument bytes = %d, want %d", len(got.(string)), 40*1024)
	}
	metadata := completeCellMetadata(t, attached[1])
	if metadata["tool_calls_complete"] != true {
		t.Fatalf("tool_calls_complete = %#v, want true", metadata["tool_calls_complete"])
	}

	// Message-budget argument truncation keeps the call inventory complete.
	bounded := model.BoundExecutedToolCallsForMessage(attached, model.ExecutedToolCallMetadataTotalBytes(attached)-1_000)
	calls = attachedCallsForItem(t, bounded[1])
	if len(calls) != 1 {
		t.Fatalf("budgeted calls = %#v, want the call retained", calls)
	}
	truncatedArguments, ok := calls[0]["arguments"].(map[string]any)
	if !ok || truncatedArguments["_codex_executed_tool_call_truncated"] == nil {
		t.Fatalf("message budget did not truncate arguments: %#v", calls[0])
	}
	metadata = completeCellMetadata(t, bounded[1])
	if metadata["tool_calls_complete"] != true {
		t.Fatalf("argument truncation cleared inventory completeness: %#v", metadata)
	}
}
