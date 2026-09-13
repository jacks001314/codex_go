package turn

import (
	"context"
	"strings"
	"testing"

	"codex_go/model"
	"codex_go/tool"
)

func directMetadataTestInvocation(callID string, arguments string) *tool.Invocation {
	return &tool.Invocation{
		CallID:   callID,
		ToolName: tool.PlainName("echo"),
		Payload:  tool.Payload{Kind: tool.PayloadFunction, Arguments: arguments},
	}
}

// Mirrors Rust's executed_tool_calls_direct_tests: a prepared direct call is
// attached to its own output, marks the inventory complete, and a permit from a
// retired recorder generation attaches nothing.
func TestExecutedToolCallRecorderBindsDirectRecordToOutputLikeRust(t *testing.T) {
	recorder := NewExecutedToolCallRecorder()
	invocation := directMetadataTestInvocation("call-1", `{"text":"hi"}`)
	call, permit := recorder.PrepareDirectCall(invocation, "")
	if call == nil || permit == nil {
		t.Fatal("PrepareDirectCall() = nil, want a reserved record")
	}
	output := &ToolResponseItem{Type: "function_call_output", CallID: "call-1", Output: NewFunctionCallOutputPayload("ok", nil)}
	recorder.AttachDirectCallToOutput(output, call, permit)
	permit.Release()
	if got := len(output.ExecutedToolCalls()); got != 1 {
		t.Fatalf("attached calls = %d, want 1", got)
	}
	object := marshalExecutedToolCallItem(t, output)
	metadata := object["internal_chat_message_metadata_passthrough"].(map[string]any)
	if metadata["tool_calls_complete"] != true {
		t.Fatalf("completeness = %#v", metadata)
	}

	// A record prepared before capture was disabled must not attach.
	staleInvocation := directMetadataTestInvocation("call-2", `{}`)
	staleCall, stalePermit := recorder.PrepareDirectCall(staleInvocation, "")
	recorder.mu.Lock()
	recorder.lifetime = newExecutedToolCallLifetime()
	recorder.mu.Unlock()
	staleOutput := &ToolResponseItem{Type: "function_call_output", CallID: "call-2", Output: NewFunctionCallOutputPayload("ok", nil)}
	recorder.AttachDirectCallToOutput(staleOutput, staleCall, stalePermit)
	stalePermit.Release()
	if got := len(staleOutput.ExecutedToolCalls()); got != 0 {
		t.Fatalf("stale record attached: %d calls", got)
	}

	// Truncated arguments cannot claim a complete inventory (Rust #45185).
	large := `{"text":"` + strings.Repeat("x", model.MaxExecutedToolCallArgumentBytes+1) + `"}`
	truncatedInvocation := directMetadataTestInvocation("call-3", large)
	truncatedCall, truncatedPermit := recorder.PrepareDirectCall(truncatedInvocation, "")
	truncatedOutput := &ToolResponseItem{Type: "function_call_output", CallID: "call-3", Output: NewFunctionCallOutputPayload("ok", nil)}
	recorder.AttachDirectCallToOutput(truncatedOutput, truncatedCall, truncatedPermit)
	truncatedPermit.Release()
	if truncatedCall == nil || !truncatedCall.Truncated() {
		t.Fatalf("large arguments were not truncated: %#v", truncatedCall)
	}
	truncatedObject := marshalExecutedToolCallItem(t, truncatedOutput)
	truncatedMetadata, _ := truncatedObject["internal_chat_message_metadata_passthrough"].(map[string]any)
	if truncatedMetadata != nil && truncatedMetadata["tool_calls_complete"] == true {
		t.Fatalf("truncated arguments claimed completeness: %#v", truncatedMetadata)
	}
}

// Mirrors Rust's retained-metadata budget: once the recorder lifetime has
// retained the budget, further direct records are dropped instead of exceeding it.
func TestExecutedToolCallRecorderBoundsRetainedDirectMetadataLikeRust(t *testing.T) {
	recorder := NewExecutedToolCallRecorder()
	arguments := `{"text":"` + strings.Repeat("x", 8000) + `"}`
	dropped := false
	for index := 0; index < 200; index++ {
		callID := "retained-" + strings.Repeat("x", index%3) + string(rune(index+1))
		invocation := directMetadataTestInvocation(callID, arguments)
		call, permit := recorder.PrepareDirectCall(invocation, "")
		if call == nil {
			t.Fatalf("PrepareDirectCall() = nil at %d", index)
		}
		output := &ToolResponseItem{Type: "function_call_output", CallID: callID, Output: NewFunctionCallOutputPayload("ok", nil)}
		recorder.AttachDirectCallToOutput(output, call, permit)
		permit.Release()
		if len(output.ExecutedToolCalls()) == 0 {
			dropped = true
		}
	}
	if !dropped {
		t.Fatal("retention budget never dropped a direct record")
	}
	recorder.mu.Lock()
	retained := recorder.retainedDirectMetadataBytes
	recorder.mu.Unlock()
	if retained > maxRetainedDirectMetadataBytes {
		t.Fatalf("retained direct metadata = %d, want <= %d", retained, maxRetainedDirectMetadataBytes)
	}
}

// Mirrors Rust's capture-change handling: disabling capture strips direct
// metadata from prompt inputs while leaving Code Mode cells intact.
func TestExecutedToolCallRecorderStripsDirectMetadataWhenDisabledLikeRust(t *testing.T) {
	recorder := NewExecutedToolCallRecorder()
	directOutput := &ToolResponseItem{Type: "function_call_output", CallID: "direct", Output: NewFunctionCallOutputPayload("ok", nil)}
	directOutput.SetExecutedToolCallsComplete(true)
	directOutput.ReplaceExecutedToolCalls([]model.ExecutedToolCall{model.NewExecutedToolCall("echo", map[string]any{"text": "hi"})})
	cellOutput := &ToolResponseItem{Type: "custom_tool_call_output", CallID: "cell-output", Output: NewFunctionCallOutputPayload("ok", nil)}
	cellOutput.SetExecutedToolCallCell("cell-1")
	cellOutput.ReplaceExecutedToolCalls([]model.ExecutedToolCall{model.NewExecutedToolCall("nested", map[string]any{})})

	recorder.StripDirectMetadataWhenDisabled([]any{directOutput, cellOutput})
	if got := len(directOutput.ExecutedToolCalls()); got != 0 {
		t.Fatalf("enabled recorder stripped direct metadata: %d calls", got)
	}

	recorder.mu.Lock()
	recorder.lifetime = nil
	recorder.mu.Unlock()
	recorder.StripDirectMetadataWhenDisabled([]any{directOutput, cellOutput})
	if got := len(directOutput.ExecutedToolCalls()); got != 0 {
		t.Fatalf("disabled recorder kept direct metadata: %d calls", got)
	}
	if got := len(cellOutput.ExecutedToolCalls()); got != 1 {
		t.Fatalf("disabled recorder stripped Code Mode metadata: %d calls", got)
	}
}

// Mirrors Rust's observe_non_dispatched_call: a call ID that bypassed dispatch
// cannot later establish Code Mode completeness when it is reused.
func TestExecutedToolCallRecorderObservesNonDispatchedCallLikeRust(t *testing.T) {
	recorder := NewExecutedToolCallRecorder()
	recorder.ObserveNonDispatchedCall(&model.AgentItem{Type: "tool_search_call", CallID: "reused", Name: "search", Arguments: `{}`})
	recorder.ObserveNonDispatchedCall(&model.AgentItem{Type: "agent_message", Text: "no call"})
	recorder.RecordToolCall(codeModeNestedInvocation("reused", "cell-1", "first"), model.ToolModeCodeMode)
	recorder.RegisterCell("cell-1", "exec-1")
	attached, attachment := recorder.AttachPendingToPrompt([]any{
		codeModeExecInputItem("exec-1"),
		&ToolResponseItem{Type: "custom_tool_call_output", CallID: "exec-1", Output: NewFunctionCallOutputPayload("ok", nil)},
	})
	if attachment == nil {
		t.Fatal("expected a Code Mode attachment")
	}
	object := marshalExecutedToolCallItem(t, attached[len(attached)-1])
	metadata := object["internal_chat_message_metadata_passthrough"].(map[string]any)
	if metadata["tool_calls_complete"] == true {
		t.Fatalf("reused non-dispatched call established completeness: %#v", metadata)
	}
}

// Mirrors Rust's dispatch attribution: the dispatcher attaches the direct record
// to the invocation's response item, and Code Mode exec/wait wrappers are not
// recorded as direct calls.
func TestToolDispatcherAttachesDirectMetadataLikeRust(t *testing.T) {
	registry := tool.NewRegistry()
	if err := registry.Register(tool.NewExecutorFunc(tool.Spec{Name: tool.PlainName("echo")}, func(context.Context, *tool.Invocation) (*tool.Output, error) {
		return &tool.Output{Success: true, Body: "ok"}, nil
	})); err != nil {
		t.Fatalf("register echo: %v", err)
	}
	recorder := NewExecutedToolCallRecorder()
	dispatcher := NewToolDispatcher(&ToolDispatcherOptions{Router: tool.NewRouter(registry), ExecutedToolCalls: recorder})
	results, err := dispatcher.ExecuteToolItems(context.Background(), []model.AgentItem{{
		Type: "function_call", CallID: "call-1", Name: "echo", Arguments: `{"text":"hi"}`,
	}})
	if err != nil {
		t.Fatalf("ExecuteToolItems() error = %v", err)
	}
	if len(results) != 1 || results[0].Response == nil {
		t.Fatalf("results = %#v", results)
	}
	if calls := results[0].Response.ExecutedToolCalls(); len(calls) != 1 || calls[0].Name != "echo" {
		t.Fatalf("direct output metadata = %#v", calls)
	}
	object := marshalExecutedToolCallItem(t, results[0].Response)
	metadata := object["internal_chat_message_metadata_passthrough"].(map[string]any)
	if metadata["tool_calls_complete"] != true {
		t.Fatalf("direct output completeness = %#v", metadata)
	}
	// The record does not stay pending, so a later prompt attach has nothing to
	// add (Rust #45185).
	if _, attachment := recorder.AttachPendingToPrompt([]any{results[0].Response}); attachment != nil {
		t.Fatalf("prompt attachment = %#v, want none", attachment)
	}
	recorder.mu.Lock()
	pending := recorder.pendingDirectCalls
	recorder.mu.Unlock()
	if pending != 0 {
		t.Fatalf("pending direct calls = %d, want 0", pending)
	}
}
