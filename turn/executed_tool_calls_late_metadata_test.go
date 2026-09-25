package turn

import (
	"strings"
	"testing"

	"codex_go/model"
	"codex_go/tool"
)

// Mirrors Rust #48222: a nested Code Mode call whose recorded arguments were
// truncated is attached to an output before its result arrives, so the recorder
// must retain that binding and let the late result update the original output
// across retries, waits and compaction.

// recordTruncatedNestedCall records a nested call whose arguments exceed the
// per-call recording limit, so the recorder stores a truncation marker instead
// of the arguments.
func recordTruncatedNestedCall(recorder *ExecutedToolCallRecorder, cellID string, callID string) {
	recorder.RecordToolCall(&tool.Invocation{
		CallID:   callID,
		ToolName: tool.NamespacedName("mcp", "echo"),
		Payload: tool.Payload{
			Kind:      tool.PayloadFunction,
			Arguments: `{"padding":"` + strings.Repeat("x", 9_000) + `"}`,
		},
		Source:  "code_mode",
		Context: map[string]any{tool.CodeModeCellIDContextKey: cellID},
	}, model.ToolModeCodeMode)
}

// lateResultInvocation is the nested invocation whose result arrives after its
// call was already recorded and attached.
func lateResultInvocation(cellID string, callID string) *tool.Invocation {
	return &tool.Invocation{
		CallID:   callID,
		ToolName: tool.PlainName("nested-tool"),
		Payload:  tool.Payload{Kind: tool.PayloadFunction, Arguments: `{}`},
		Source:   "code_mode",
		Context:  map[string]any{tool.CodeModeCellIDContextKey: cellID},
	}
}

func codeModeExecOutputItem(callID string) *ToolResponseItem {
	return &ToolResponseItem{Type: "custom_tool_call_output", CallID: callID, Output: NewFunctionCallOutputPayload("running", nil)}
}

func codeModeWaitOutputItem(callID string) *ToolResponseItem {
	return &ToolResponseItem{Type: "function_call_output", CallID: callID, Output: NewFunctionCallOutputPayload("done", nil)}
}

// attachedCallsForItem returns the bounded executed tool calls an attached
// prompt item would carry to the model.
func attachedCallsForItem(t *testing.T, item any) []map[string]any {
	t.Helper()
	bounded := model.BoundExecutedToolCallsForPrompt([]any{item})
	return executedToolCallsFromObject(t, marshalExecutedToolCallItem(t, bounded[0]))
}

func callResultMetadata(call map[string]any) any {
	if call == nil {
		return nil
	}
	return call["tool_result_metadata"]
}

// attachedCallsOrNil returns an attached item's recorded calls, or nil when the
// item carries no executed-tool-call metadata at all.
func attachedCallsOrNil(t *testing.T, item any) []map[string]any {
	t.Helper()
	bounded := model.BoundExecutedToolCallsForPrompt([]any{item})
	object := marshalExecutedToolCallItem(t, bounded[0])
	metadata, ok := object["internal_chat_message_metadata_passthrough"].(map[string]any)
	if !ok {
		return nil
	}
	rawCalls, ok := metadata["executed_tool_calls"].([]any)
	if !ok {
		return nil
	}
	calls := make([]map[string]any, 0, len(rawCalls))
	for _, raw := range rawCalls {
		call, _ := raw.(map[string]any)
		calls = append(calls, call)
	}
	return calls
}

func TestExecutedToolCallRecorderPreservesLateTruncatedResultMetadataLikeRust(t *testing.T) {
	recorder := NewExecutedToolCallRecorder()
	recordTruncatedNestedCall(recorder, "late-truncated-cell", "first")
	recordTruncatedNestedCall(recorder, "late-truncated-cell", "second")
	recorder.StartCell("late-truncated-cell", "exec")

	history := []any{codeModeExecInputItem("exec"), codeModeExecOutputItem("exec")}
	_, token := recorder.AttachPendingToPrompt(history)
	if token == nil {
		t.Fatal("the truncated inventory must attach to the exec output")
	}
	recorder.CommitAttachment(token)

	// The results arrive only after the truncated calls were attached.
	if !recorder.RecordToolResultMetadata(lateResultInvocation("late-truncated-cell", "second"), map[string]any{"provider": "second"}) {
		t.Fatal("late result metadata for the second call was dropped")
	}
	if !recorder.RecordToolResultMetadata(lateResultInvocation("late-truncated-cell", "first"), map[string]any{"provider": "first"}) {
		t.Fatal("late result metadata for the first call was dropped")
	}

	// Both records keep their truncation marker and the late result on every
	// later request, in order (Rust late_result_metadata_survives_truncated_arguments).
	for attempt := 0; attempt < 2; attempt++ {
		retry, retryToken := recorder.AttachPendingToPrompt(history)
		if retryToken != nil {
			t.Fatalf("attempt %d consumed pending state again: %#v", attempt, retryToken)
		}
		calls := attachedCallsForItem(t, retry[1])
		if len(calls) != 2 {
			t.Fatalf("attempt %d attached %d calls, want 2", attempt, len(calls))
		}
		if calls[0]["name"] != "mcp__echo" || calls[1]["name"] != "mcp__echo" {
			t.Fatalf("attempt %d call names = %#v", attempt, calls)
		}
		arguments, _ := calls[0]["arguments"].(map[string]any)
		if _, truncated := arguments["_codex_executed_tool_call_truncated"]; !truncated {
			t.Fatalf("attempt %d first call lost its truncation marker: %#v", attempt, calls[0]["arguments"])
		}
		if got := callResultMetadata(calls[0]); !metadataEqual(got, map[string]any{"provider": "first"}) {
			t.Fatalf("attempt %d first call metadata = %#v", attempt, got)
		}
		if got := callResultMetadata(calls[1]); !metadataEqual(got, map[string]any{"provider": "second"}) {
			t.Fatalf("attempt %d second call metadata = %#v", attempt, got)
		}
	}

	compact := recorder.AttachToCompactionPrompt(history, true)
	calls := attachedCallsForItem(t, compact[1])
	if len(calls) != 2 || !metadataEqual(callResultMetadata(calls[1]), map[string]any{"provider": "second"}) {
		t.Fatalf("compaction prompt lost the late result: %#v", calls)
	}
}

func TestExecutedToolCallRecorderLateTruncatedMetadataSurvivesSubsequentWaitsLikeRust(t *testing.T) {
	for _, outcome := range []struct {
		resultBeforeWait bool
		callBeforeWait   bool
		closeBeforeWait  bool
	}{
		{false, false, false},
		{false, false, true},
		{false, true, false},
		{false, true, true},
		{true, false, false},
		{true, false, true},
		{true, true, false},
		{true, true, true},
	} {
		name := "waits"
		if outcome.resultBeforeWait {
			name += "-result-first"
		}
		if outcome.callBeforeWait {
			name += "-call-first"
		}
		if outcome.closeBeforeWait {
			name += "-closed"
		}
		t.Run(name, func(t *testing.T) {
			recorder := NewExecutedToolCallRecorder()
			recorder.StartCell("live-cell", "exec")
			recordTruncatedNestedCall(recorder, "live-cell", "nested")
			history := []any{codeModeExecInputItem("exec"), codeModeExecOutputItem("exec")}
			if _, token := recorder.AttachPendingToPrompt(history); token == nil {
				t.Fatal("the nested call did not attach to the exec output")
			} else {
				recorder.CommitAttachment(token)
			}

			metadata := map[string]any{"provider": "late"}
			if outcome.resultBeforeWait {
				if !recorder.RecordToolResultMetadata(lateResultInvocation("live-cell", "nested"), metadata) {
					t.Fatal("late result was dropped")
				}
			}
			if outcome.callBeforeWait {
				recorder.RecordToolCall(codeModeNestedInvocation("later", "live-cell", "later"), model.ToolModeCodeMode)
				recorder.RegisterCell("live-cell", "wait-1")
			} else {
				recorder.RegisterCell("live-cell", "wait-1")
				recorder.RecordToolCall(codeModeNestedInvocation("later", "live-cell", "later"), model.ToolModeCodeMode)
			}
			if !outcome.resultBeforeWait {
				if !recorder.RecordToolResultMetadata(lateResultInvocation("live-cell", "nested"), metadata) {
					t.Fatal("late result was dropped after a wait")
				}
			}

			history = append(history, codeModeWaitInputItem("wait-1", "live-cell"), codeModeWaitOutputItem("wait-1"))
			request, _ := recorder.AttachPendingToPrompt(history)
			execCalls := attachedCallsForItem(t, request[1])
			if len(execCalls) != 1 || !metadataEqual(callResultMetadata(execCalls[0]), metadata) {
				t.Fatalf("exec output lost the late result: %#v", execCalls)
			}
			waitCalls := attachedCallsForItem(t, request[3])
			if len(waitCalls) != 1 || waitCalls[0]["arguments"].(map[string]any)["message"] != "later" {
				t.Fatalf("wait output calls = %#v", waitCalls)
			}

			// A later wait re-emits the original output with the late result.
			// The dispatch gate may close before or after the final wait registers
			// (Rust late_truncated_metadata_survives_subsequent_waits).
			if outcome.closeBeforeWait {
				recorder.FinishCell("live-cell")
			}
			recorder.RegisterCell("live-cell", "wait-2")
			recorder.FinishCell("live-cell")
			recorder.FinishCell("live-cell")
			history = append(history, codeModeWaitInputItem("wait-2", "live-cell"), codeModeWaitOutputItem("wait-2"))
			later, _ := recorder.AttachPendingToPrompt(history)
			laterCalls := attachedCallsForItem(t, later[1])
			if len(laterCalls) != 1 || !metadataEqual(callResultMetadata(laterCalls[0]), metadata) {
				t.Fatalf("later wait lost the late result: %#v", laterCalls)
			}
		})
	}
}

func TestExecutedToolCallRecorderClearsPendingCallsWhenRuntimeCellIsReusedLikeRust(t *testing.T) {
	recorder := NewExecutedToolCallRecorder()
	recorder.StartCell("duplicate-cell", "old-exec")
	for index := 0; index < maxPendingExecutedToolCalls; index++ {
		recorder.RecordToolCall(codeModeNestedInvocation("duplicate", "duplicate-cell", "dup"), model.ToolModeCodeMode)
	}
	// A repeated invocation ID replaces its attempt instead of consuming the
	// pending budget again (Rust duplicate_pending_ids_do_not_consume_extra_capacity).
	if got := recorder.pendingNestedCalls(); got != 1 {
		t.Fatalf("pending nested calls = %d, want 1", got)
	}
	group := recorder.groups["cell:duplicate-cell"]
	if group == nil || len(group.pending) != 1 {
		t.Fatalf("duplicate cell = %#v", group)
	}
	if group.truncatedMetadataBindingValid {
		t.Fatal("a repeated nested ID must revoke the late-result binding")
	}

	// A reused runtime handle releases the previous execution's records instead
	// of attaching them to the new one (Rust reused_cell_does_not_attach_old_pending_calls_to_new_exec).
	recorder.StartCell("duplicate-cell", "new-exec")
	if got := recorder.pendingNestedCalls(); got != 0 {
		t.Fatalf("pending nested calls after reuse = %d, want 0", got)
	}
	recorder.RecordToolCall(codeModeNestedInvocation("new", "duplicate-cell", "new"), model.ToolModeCodeMode)
	if got := recorder.pendingNestedCalls(); got != 1 {
		t.Fatalf("pending nested calls for the new execution = %d, want 1", got)
	}
}

func TestExecutedToolCallRecorderDoesNotAttributeLateTruncatedMetadataAmbiguouslyLikeRust(t *testing.T) {
	for _, scenario := range []string{
		"duplicate_output",
		"wrong_input",
		"wrong_sibling_input",
		"duplicate_id",
		"reused_cell",
		"dispatch_after_close",
	} {
		t.Run(scenario, func(t *testing.T) {
			recorder := NewExecutedToolCallRecorder()
			recorder.StartCell("late-truncated-cell", "exec")
			recordTruncatedNestedCall(recorder, "late-truncated-cell", "nested")
			history := []any{codeModeExecInputItem("exec"), codeModeExecOutputItem("exec")}
			if _, token := recorder.AttachPendingToPrompt(history); token == nil {
				t.Fatal("the nested call did not attach to the exec output")
			} else {
				recorder.CommitAttachment(token)
			}
			if !recorder.RecordToolResultMetadata(lateResultInvocation("late-truncated-cell", "nested"), map[string]any{"provider": "late"}) {
				t.Fatal("late result was dropped")
			}

			retry := append([]any(nil), history...)
			switch scenario {
			case "duplicate_output":
				retry = append(retry, codeModeExecOutputItem("exec"))
			case "wrong_input":
				retry[0] = codeModeWaitInputItem("exec", "late-truncated-cell")
			case "wrong_sibling_input":
				recorder.RegisterCell("late-truncated-cell", "wait")
				retry = append(retry, codeModeWaitInputItem("wait", "different-cell"), codeModeWaitOutputItem("wait"))
			case "duplicate_id":
				recordTruncatedNestedCall(recorder, "late-truncated-cell", "nested")
			case "reused_cell":
				recorder.StartCell("late-truncated-cell", "another-exec")
			case "dispatch_after_close":
				recorder.FinishCell("late-truncated-cell")
				recorder.RecordToolCall(codeModeNestedInvocation("after-close", "late-truncated-cell", "late"), model.ToolModeCodeMode)
			}

			attached, _ := recorder.AttachPendingToPrompt(retry)
			for index := 1; index < len(attached); index++ {
				itemType, _, ok := executedToolCallOutputIdentity(attached[index])
				if !ok || strings.TrimSpace(itemType) == "" {
					continue
				}
				for _, call := range attachedCallsOrNil(t, attached[index]) {
					if callResultMetadata(call) != nil {
						t.Fatalf("%s: late metadata was attributed to an ambiguous output: %#v", scenario, call)
					}
				}
			}
		})
	}
}

func metadataEqual(got any, want map[string]any) bool {
	object, ok := got.(map[string]any)
	if !ok || len(object) != len(want) {
		return false
	}
	for key, value := range want {
		if object[key] != value {
			return false
		}
	}
	return true
}
