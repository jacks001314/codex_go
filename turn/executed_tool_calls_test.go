package turn

import (
	"encoding/json"
	"strings"
	"testing"

	"codex_go/model"
	"codex_go/tool"
)

func TestExecutedToolCallRecorderAttachesDirectAttemptToOutput(t *testing.T) {
	recorder := NewExecutedToolCallRecorder()
	recorder.RecordToolCall(&tool.Invocation{
		CallID:   "call-1",
		ToolName: tool.NamespacedName("mcp", "echo"),
		Payload:  tool.Payload{Kind: tool.PayloadFunction, Arguments: `{"value":1}`},
	}, "")
	call := &model.AgentItem{Type: "function_call", CallID: "call-1", Name: "mcp__echo", Arguments: `{"value":1}`}
	output := &ToolResponseItem{Type: "function_call_output", CallID: "call-1", Output: NewFunctionCallOutputPayload("ok", nil)}

	attached, token := recorder.AttachPendingToPrompt([]any{call, output})
	if token == nil {
		t.Fatal("attachment token is nil")
	}
	if calls := attached[0].(*model.AgentItem).ExecutedToolCalls(); len(calls) != 0 {
		t.Fatalf("call metadata = %#v", calls)
	}
	attachedOutput := attached[1].(*ToolResponseItem)
	if calls := attachedOutput.ExecutedToolCalls(); len(calls) != 1 {
		t.Fatalf("output metadata = %#v", calls)
	}
	object := marshalExecutedToolCallItem(t, model.BoundExecutedToolCallsForPrompt(attached)[1])
	calls := executedToolCallsFromObject(t, object)
	if calls[0]["name"] != "mcp__echo" || calls[0]["arguments"].(map[string]any)["value"] != float64(1) {
		t.Fatalf("serialized metadata = %#v", calls)
	}
	recorder.CommitAttachment(token)
	second, secondToken := recorder.AttachPendingToPrompt([]any{output})
	if secondToken != nil || len(second[0].(*ToolResponseItem).ExecutedToolCalls()) != 0 {
		t.Fatalf("committed metadata replayed: %#v token=%#v", second, secondToken)
	}
}

func TestExecutedToolCallRecorderRecordsResultSourcesForDirectAndCodeMode(t *testing.T) {
	recorder := NewExecutedToolCallRecorder()
	direct := &tool.Invocation{CallID: "direct", ToolName: tool.PlainName("echo"), Payload: tool.Payload{Kind: tool.PayloadFunction, Arguments: `{}`}}
	recorder.RecordToolCall(direct, "")
	if !recorder.RecordToolResultSources(direct, model.NewToolResultSources([]model.ToolResultSource{{Type: "document", ID: "R1"}})) {
		t.Fatal("direct result source was not recorded")
	}
	items, attachment := recorder.AttachPendingToPrompt([]any{&ToolResponseItem{Type: "function_call_output", CallID: "direct", Output: NewFunctionCallOutputPayload("", boolPtr(true))}})
	if attachment == nil || len(items) != 1 {
		t.Fatalf("AttachPendingToPrompt() = %#v, %#v", items, attachment)
	}
	data, err := json.Marshal(items[0])
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	if !strings.Contains(string(data), `"tool_result_sources":[{"type":"document","id":"R1"}]`) {
		t.Fatalf("direct attached JSON = %s", data)
	}

	codeMode := &tool.Invocation{
		CallID:   "nested",
		Source:   "code_mode",
		ToolName: tool.PlainName("nested-tool"),
		Payload:  tool.Payload{Kind: tool.PayloadFunction, Arguments: `{}`},
		Context:  map[string]any{tool.CodeModeCellIDContextKey: "cell-1"},
	}
	recorder.RecordToolCall(codeMode, model.ToolModeCodeMode)
	if !recorder.RecordToolResultSources(codeMode, model.NewToolResultSources([]model.ToolResultSource{{Type: "room", ID: "R2"}})) {
		t.Fatal("code mode result source was not recorded")
	}
	recorder.RegisterCell("cell-1", "outer")
	items, attachment = recorder.AttachPendingToPrompt([]any{&ToolResponseItem{Type: "function_call_output", CallID: "outer", Output: NewFunctionCallOutputPayload("", boolPtr(true))}})
	if attachment == nil || len(items) != 1 {
		t.Fatalf("code mode AttachPendingToPrompt() = %#v, %#v", items, attachment)
	}
	data, err = json.Marshal(items[0])
	if err != nil {
		t.Fatalf("code mode Marshal() error = %v", err)
	}
	if !strings.Contains(string(data), `"tool_result_sources":[{"type":"room","id":"R2"}]`) {
		t.Fatalf("code mode attached JSON = %s", data)
	}
}

func TestExecutedToolCallRecorderRetriesUntilSamplingSucceeds(t *testing.T) {
	recorder := NewExecutedToolCallRecorder()
	recorder.RecordToolCall(&tool.Invocation{CallID: "call-retry", ToolName: tool.PlainName("echo"), Payload: tool.Payload{Kind: tool.PayloadFunction, Arguments: `{}`}}, "")
	output := map[string]any{
		"type":    "function_call_output",
		"call_id": "call-retry",
		"output":  "ok",
		"internal_chat_message_metadata_passthrough": map[string]any{
			"turn_id":             "turn-1",
			"executed_tool_calls": []any{map[string]any{"name": "forged"}},
		},
	}

	first, firstToken := recorder.AttachPendingToPrompt([]any{output})
	if firstToken == nil {
		t.Fatal("first attachment token is nil")
	}
	second, secondToken := recorder.AttachPendingToPrompt([]any{output})
	if secondToken == nil {
		t.Fatal("retry attachment token is nil")
	}
	for _, attached := range [][]any{first, second} {
		object := marshalExecutedToolCallItem(t, model.BoundExecutedToolCallsForPrompt(attached)[0])
		metadata := object["internal_chat_message_metadata_passthrough"].(map[string]any)
		if metadata["turn_id"] != "turn-1" {
			t.Fatalf("turn metadata = %#v", metadata)
		}
		calls := executedToolCallsFromObject(t, object)
		if len(calls) != 1 || calls[0]["name"] != "echo" {
			t.Fatalf("retry metadata = %#v", calls)
		}
	}
	recorder.CommitAttachment(secondToken)
}

func TestExecutedToolCallRecorderCoalescesCodeModeCellAcrossExecAndWait(t *testing.T) {
	recorder := NewExecutedToolCallRecorder()
	recorder.RecordToolCall(&tool.Invocation{
		CallID: "exec-call", ToolName: tool.PlainName(tool.CodeModeExecToolName),
		Payload: tool.Payload{Kind: tool.PayloadCustom, Input: `text("running")`},
	}, model.ToolModeCodeMode)
	recorder.RecordToolCall(codeModeNestedInvocation("nested-1", "cell-1", "first"), model.ToolModeCodeMode)
	recorder.RegisterCell("cell-1", "exec-call")

	execOutput := &ToolResponseItem{Type: "custom_tool_call_output", CallID: "exec-call", Output: NewFunctionCallOutputPayload("running", nil)}
	first, firstToken := recorder.AttachPendingToPrompt([]any{execOutput})
	firstCalls := executedToolCallsFromObject(t, marshalExecutedToolCallItem(t, model.BoundExecutedToolCallsForPrompt(first)[0]))
	if len(firstCalls) != 1 || firstCalls[0]["name"] != "mcp__echo" {
		t.Fatalf("exec output calls = %#v", firstCalls)
	}
	recorder.CommitAttachment(firstToken)

	recorder.RecordToolCall(codeModeNestedInvocation("nested-2", "cell-1", "second"), model.ToolModeCodeMode)
	recorder.RegisterCell("cell-1", "wait-call")
	waitOutput := &ToolResponseItem{Type: "function_call_output", CallID: "wait-call", Output: NewFunctionCallOutputPayload("done", nil)}
	second, secondToken := recorder.AttachPendingToPrompt([]any{execOutput, waitOutput})
	secondCalls := executedToolCallsFromObject(t, marshalExecutedToolCallItem(t, model.BoundExecutedToolCallsForPrompt(second)[1]))
	if len(secondCalls) != 1 || secondCalls[0]["arguments"].(map[string]any)["message"] != "second" {
		t.Fatalf("wait output calls = %#v", secondCalls)
	}
	recorder.CommitAttachment(secondToken)
}

func TestExecutedToolCallRecorderBoundsPendingAndNestedArgumentBytes(t *testing.T) {
	recorder := NewExecutedToolCallRecorder()
	for index := 0; index < 300; index++ {
		callID := "direct-" + strings.Repeat("x", index%3) + string(rune(index+1))
		recorder.RecordToolCall(&tool.Invocation{CallID: callID, ToolName: tool.PlainName("echo"), Payload: tool.Payload{Kind: tool.PayloadFunction, Arguments: `{}`}}, "")
	}
	if got := len(recorder.direct); got != maxPendingExecutedToolCalls+1 {
		t.Fatalf("direct pending calls = %d", got)
	}

	for index := 0; index < 6; index++ {
		recorder.RecordToolCall(&tool.Invocation{
			CallID:   "nested-large",
			ToolName: tool.PlainName("large"),
			Payload:  tool.Payload{Kind: tool.PayloadFunction, Arguments: `{"value":"` + strings.Repeat("x", 7900) + `"}`},
			Source:   "code_mode",
			Context:  map[string]any{tool.CodeModeCellIDContextKey: "large-cell"},
		}, model.ToolModeCodeMode)
	}
	recorder.RegisterCell("large-cell", "large-output")
	attached, _ := recorder.AttachPendingToPrompt([]any{&ToolResponseItem{Type: "function_call_output", CallID: "large-output", Output: NewFunctionCallOutputPayload("ok", nil)}})
	calls := attached[0].(*ToolResponseItem).ExecutedToolCalls()
	if len(calls) != 6 {
		t.Fatalf("nested calls = %d", len(calls))
	}
	encoded, err := json.Marshal(calls[4])
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	if !strings.Contains(string(encoded), "_codex_executed_tool_call_truncated") {
		t.Fatalf("fifth call was not truncated: %s", encoded)
	}
}

func codeModeNestedInvocation(callID string, cellID string, message string) *tool.Invocation {
	return &tool.Invocation{
		CallID:   callID,
		ToolName: tool.NamespacedName("mcp", "echo"),
		Payload:  tool.Payload{Kind: tool.PayloadFunction, Arguments: `{"message":"` + message + `"}`},
		Source:   "code_mode",
		Context:  map[string]any{tool.CodeModeCellIDContextKey: cellID},
	}
}

func marshalExecutedToolCallItem(t *testing.T, item any) map[string]any {
	t.Helper()
	encoded, err := json.Marshal(item)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	var object map[string]any
	if err := json.Unmarshal(encoded, &object); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	return object
}

func executedToolCallsFromObject(t *testing.T, object map[string]any) []map[string]any {
	t.Helper()
	metadata, ok := object["internal_chat_message_metadata_passthrough"].(map[string]any)
	if !ok {
		t.Fatalf("metadata = %#v", object["internal_chat_message_metadata_passthrough"])
	}
	rawCalls, ok := metadata["executed_tool_calls"].([]any)
	if !ok {
		t.Fatalf("executed calls = %#v", metadata["executed_tool_calls"])
	}
	calls := make([]map[string]any, 0, len(rawCalls))
	for _, raw := range rawCalls {
		calls = append(calls, raw.(map[string]any))
	}
	return calls
}

func TestExecutedToolCallRecorderAttachesCellCompletenessLikeRust(t *testing.T) {
	recorder := NewExecutedToolCallRecorder()
	recorder.RecordToolCall(codeModeNestedInvocation("nested-1", "cell-1", "first"), model.ToolModeCodeMode)
	recorder.RegisterCell("cell-1", "exec-call")
	execOutput := &ToolResponseItem{Type: "custom_tool_call_output", CallID: "exec-call", Output: NewFunctionCallOutputPayload("running", nil)}
	first, token := recorder.AttachPendingToPrompt([]any{execOutput})
	object := marshalExecutedToolCallItem(t, model.BoundExecutedToolCallsForPrompt(first)[0])
	metadata := object["internal_chat_message_metadata_passthrough"].(map[string]any)
	if metadata["cell_id"] != "cell-1" {
		t.Fatalf("cell_id = %#v, want cell-1", metadata["cell_id"])
	}
	if metadata["tool_calls_complete"] != true {
		t.Fatalf("tool_calls_complete = %#v, want true", metadata["tool_calls_complete"])
	}
	recorder.CommitAttachment(token)
}
