package turn

import (
	"strconv"
	"strings"
	"testing"

	"codex_go/model"
)

// Mirrors Rust #48222
// late_truncated_metadata_survives_close_before_first_attachment: closing the
// dispatch gate before the first successful attachment must not discard the
// pending truncated call or its late result. The exec output keeps the call;
// later waits only carry the cell and completion marker.
func TestExecutedToolCallRecorderPreservesLateMetadataAcrossCloseBeforeFirstAttachmentLikeRust(t *testing.T) {
	recorder := NewExecutedToolCallRecorder()
	// Establish a new thread's history-free first prompt so a later wait can
	// prove completion, matching Rust's InitialHistory::New.
	if _, token := recorder.AttachPendingToPrompt([]any{
		map[string]any{"type": "message", "role": "user", "content": "hi"},
	}); token != nil {
		t.Fatal("history-free attach must not produce an attachment")
	}
	recorder.StartCell("closed-cell", "exec")
	recordTruncatedNestedCall(recorder, "closed-cell", "nested")
	recorder.FinishCell("closed-cell")

	history := []any{codeModeExecInputItem("exec"), codeModeExecOutputItem("exec")}
	if _, token := recorder.AttachPendingToPrompt(history); token == nil {
		t.Fatal("the closed cell's truncated inventory did not attach")
	}
	metadata := map[string]any{"provider": "late"}
	if !recorder.RecordToolResultMetadata(lateResultInvocation("closed-cell", "nested"), metadata) {
		t.Fatal("the late result was dropped after the cell closed")
	}

	for _, waitCallID := range []string{"wait-1", "wait-2"} {
		recorder.FinishCell("closed-cell")
		recorder.RegisterCell("closed-cell", waitCallID)
		history = append(history, codeModeWaitInputItem(waitCallID, "closed-cell"), codeModeWaitOutputItem(waitCallID))

		attached, _ := recorder.AttachPendingToPrompt(history)
		calls := attachedCallsForItem(t, attached[1])
		if len(calls) != 1 || !metadataEqual(callResultMetadata(calls[0]), metadata) {
			t.Fatalf("%s exec output lost the late result: %#v", waitCallID, calls)
		}
		waitCalls := attachedCallsOrNil(t, attached[len(attached)-1])
		if len(waitCalls) != 0 {
			t.Fatalf("%s wait output inherited the original call: %#v", waitCallID, waitCalls)
		}
		waitObject := marshalExecutedToolCallItem(t, attached[len(attached)-1])
		waitMetadata, _ := waitObject["internal_chat_message_metadata_passthrough"].(map[string]any)
		if waitMetadata["cell_id"] != "exec" || waitMetadata["tool_calls_complete"] != true {
			t.Fatalf("%s wait metadata = %#v", waitCallID, waitMetadata)
		}
	}
}

// Mirrors Rust #48222 closed_truncated_cell_is_reclaimed_safely_under_pressure:
// capacity pressure may discard a closed cell's retained binding, but must not
// attach the now-untrusted late metadata to the output that is still present.
func TestExecutedToolCallRecorderReclaimsClosedTruncatedCellSafelyLikeRust(t *testing.T) {
	recorder := NewExecutedToolCallRecorder()
	recorder.StartCell("old-cell", "old-exec")
	recordTruncatedNestedCall(recorder, "old-cell", "nested")
	recorder.FinishCell("old-cell")

	history := []any{codeModeExecInputItem("old-exec"), codeModeExecOutputItem("old-exec")}
	_, token := recorder.AttachPendingToPrompt(history)
	if token == nil {
		t.Fatal("the old cell's truncated inventory did not attach")
	}
	recorder.CommitAttachment(token)
	metadata := map[string]any{"provider": "late"}
	if !recorder.RecordToolResultMetadata(lateResultInvocation("old-cell", "nested"), metadata) {
		t.Fatal("the first late result was dropped")
	}

	for index := 0; index < maxPendingExecutedToolCalls; index++ {
		recorder.StartCell("live-cell-"+strconv.Itoa(index), "live-exec-"+strconv.Itoa(index))
	}
	if recorder.groups["cell:old-cell"] != nil {
		t.Fatal("capacity pressure did not reclaim the closed cell")
	}
	if recorder.RecordToolResultMetadata(lateResultInvocation("old-cell", "nested"), metadata) {
		t.Fatal("a reclaimed cell accepted late metadata")
	}

	attached, _ := recorder.AttachPendingToPrompt(history)
	calls := attachedCallsForItem(t, attached[1])
	if len(calls) != 1 {
		t.Fatalf("reclaimed output calls = %#v, want the original truncated call", calls)
	}
	if callResultMetadata(calls[0]) != nil {
		t.Fatalf("reclaimed output kept untrusted late metadata: %#v", calls[0])
	}
}

// Mirrors Rust #48222 late_truncated_metadata_preserves_large_result_on_retry:
// a large late result is preserved in full by the recorder. Wire-level request
// budgeting happens later and must not change the retained observation.
func TestExecutedToolCallRecorderPreservesLargeLateTruncatedMetadataLikeRust(t *testing.T) {
	recorder := NewExecutedToolCallRecorder()
	recorder.StartCell("late-truncated-cell", "exec")
	recordTruncatedNestedCall(recorder, "late-truncated-cell", "nested")

	history := []any{codeModeExecInputItem("exec"), codeModeExecOutputItem("exec")}
	_, token := recorder.AttachPendingToPrompt(history)
	if token == nil {
		t.Fatal("the truncated inventory did not attach")
	}
	recorder.CommitAttachment(token)

	metadata := map[string]any{
		"openai/resource_access": map[string]any{"resources": []any{"result"}},
		"optional":               strings.Repeat("x", 2*1024*1024),
	}
	if !recorder.RecordToolResultMetadata(lateResultInvocation("late-truncated-cell", "nested"), metadata) {
		t.Fatal("the late result was dropped")
	}
	for attempt := 0; attempt < 2; attempt++ {
		attached, _ := recorder.AttachPendingToPrompt(history)
		object := marshalExecutedToolCallItem(t, attached[1])
		metadataObject := object["internal_chat_message_metadata_passthrough"].(map[string]any)
		rawCalls := metadataObject["executed_tool_calls"].([]any)
		if len(rawCalls) != 1 {
			t.Fatalf("attempt %d attached %d calls, want 1", attempt, len(rawCalls))
		}
		rawCall := rawCalls[0].(map[string]any)
		if got := rawCall["tool_result_metadata"]; !metadataEqual(got, metadata) {
			t.Fatalf("attempt %d late metadata = %#v, want %#v", attempt, got, metadata)
		}
	}
}

// Mirrors Rust #48222
// late_truncated_metadata_with_wrapped_arguments_rejects_ambiguous_outputs:
// wrapped recorded arguments can be shortened by the message budget, so a
// duplicate output must still revoke the binding and clear late metadata.
func TestExecutedToolCallRecorderRejectsLateMetadataForWrappedAmbiguousOutputsLikeRust(t *testing.T) {
	recorder := NewExecutedToolCallRecorder()
	recorder.StartCell("late-truncated-cell", "exec")
	recordTruncatedNestedCall(recorder, "late-truncated-cell", "nested")

	wrapped := model.NewExecutedToolCall("other", map[string]any{
		"_codex_executed_tool_call_truncated": true,
		"padding":                             strings.Repeat("x", 8_120),
	})
	recorder.recordNested("cell:late-truncated-cell", "other", wrapped, 8_120)

	history := []any{codeModeExecInputItem("exec"), codeModeExecOutputItem("exec")}
	_, token := recorder.AttachPendingToPrompt(history)
	if token == nil {
		t.Fatal("the truncated inventory did not attach")
	}
	recorder.CommitAttachment(token)
	metadata := map[string]any{
		"openai/resource_access": map[string]any{"resources": []any{"result"}},
		"other":                  strings.Repeat("x", 40*1024),
	}
	if !recorder.RecordToolResultMetadata(lateResultInvocation("late-truncated-cell", "nested"), metadata) {
		t.Fatal("the late result was dropped before ambiguity was introduced")
	}

	ambiguous := []any{history[0], history[1], history[1]}
	attached, _ := recorder.AttachPendingToPrompt(ambiguous)
	for _, item := range attached[1:] {
		for _, call := range attachedCallsOrNil(t, item) {
			if callResultMetadata(call) != nil {
				t.Fatalf("ambiguous output kept late metadata: %#v", call)
			}
		}
	}
}

// Mirrors Rust #48222 reused_cell_does_not_attach_old_pending_calls_to_new_exec:
// a reused runtime cell must not inherit an uncommitted attachment, and its
// late result evidence becomes untrusted even before CommitAttachment.
func TestExecutedToolCallRecorderClearsProvisionalMetadataOnReuseBeforeCommitLikeRust(t *testing.T) {
	recorder := NewExecutedToolCallRecorder()
	recorder.StartCell("reused-cell", "old-exec")
	recordTruncatedNestedCall(recorder, "reused-cell", "old")
	history := []any{codeModeExecInputItem("old-exec"), codeModeExecOutputItem("old-exec")}
	if _, token := recorder.AttachPendingToPrompt(history); token == nil {
		t.Fatal("the old execution's truncated inventory did not attach")
	}
	if !recorder.RecordToolResultMetadata(lateResultInvocation("reused-cell", "old"), map[string]any{"provider": "late"}) {
		t.Fatal("the first late result was dropped")
	}

	recorder.StartCell("reused-cell", "new-exec")
	recorder.RecordToolCall(codeModeNestedInvocation("new", "reused-cell", "new"), model.ToolModeCodeMode)
	history = append(history, codeModeExecInputItem("new-exec"), codeModeExecOutputItem("new-exec"))

	attached, _ := recorder.AttachPendingToPrompt(history)
	oldCalls := attachedCallsForItem(t, attached[1])
	if len(oldCalls) != 1 || callResultMetadata(oldCalls[0]) != nil {
		t.Fatalf("reused cell kept untrusted old metadata: %#v", oldCalls)
	}
	newCalls := attachedCallsForItem(t, attached[3])
	if len(newCalls) != 1 {
		t.Fatalf("new exec calls = %#v, want only the new call", newCalls)
	}
	if arguments, _ := newCalls[0]["arguments"].(map[string]any); arguments["message"] != "new" {
		t.Fatalf("new exec inherited an old call: %#v", newCalls)
	}
	if recorder.RecordToolResultMetadata(lateResultInvocation("reused-cell", "old"), map[string]any{"provider": "later"}) {
		t.Fatal("a reused cell accepted metadata for its previous execution")
	}
}

// Mirrors Rust #48222 late_truncated_metadata_is_not_attached_to_ambiguous_outputs:
// a duplicate output revokes an uncommitted binding before any retry or wait can
// re-emit its late metadata.
func TestExecutedToolCallRecorderRejectsProvisionalMetadataForDuplicateOutputsLikeRust(t *testing.T) {
	recorder := NewExecutedToolCallRecorder()
	recorder.StartCell("ambiguous-cell", "exec")
	recordTruncatedNestedCall(recorder, "ambiguous-cell", "nested")
	history := []any{codeModeExecInputItem("exec"), codeModeExecOutputItem("exec")}
	if _, token := recorder.AttachPendingToPrompt(history); token == nil {
		t.Fatal("the truncated inventory did not attach")
	}
	if !recorder.RecordToolResultMetadata(lateResultInvocation("ambiguous-cell", "nested"), map[string]any{"provider": "late"}) {
		t.Fatal("the late result was dropped before ambiguity was introduced")
	}

	ambiguous := []any{history[0], history[1], history[1]}
	attached, _ := recorder.AttachPendingToPrompt(ambiguous)
	for index := 1; index < len(attached); index++ {
		for _, call := range attachedCallsOrNil(t, attached[index]) {
			if callResultMetadata(call) != nil {
				t.Fatalf("ambiguous output %d kept provisional metadata: %#v", index, call)
			}
		}
	}
	if recorder.RecordToolResultMetadata(lateResultInvocation("ambiguous-cell", "nested"), map[string]any{"provider": "later"}) {
		t.Fatal("an ambiguous binding accepted another late result")
	}

	retry, _ := recorder.AttachPendingToPrompt(history)
	calls := attachedCallsForItem(t, retry[1])
	if len(calls) != 1 || callResultMetadata(calls[0]) != nil {
		t.Fatalf("retry restored revoked provisional metadata: %#v", calls)
	}
}

// Mirrors Rust #48222 duplicate_id handling: a repeated nested invocation ID
// invalidates the uncommitted binding and clears its late-backfilled result.
func TestExecutedToolCallRecorderRejectsProvisionalMetadataAfterDuplicateNestedIDLikeRust(t *testing.T) {
	recorder := NewExecutedToolCallRecorder()
	recorder.StartCell("duplicate-cell", "exec")
	recordTruncatedNestedCall(recorder, "duplicate-cell", "nested")
	history := []any{codeModeExecInputItem("exec"), codeModeExecOutputItem("exec")}
	if _, token := recorder.AttachPendingToPrompt(history); token == nil {
		t.Fatal("the truncated inventory did not attach")
	}
	metadata := map[string]any{"provider": "late"}
	if !recorder.RecordToolResultMetadata(lateResultInvocation("duplicate-cell", "nested"), metadata) {
		t.Fatal("the late result was dropped before the duplicate arrived")
	}

	recordTruncatedNestedCall(recorder, "duplicate-cell", "nested")
	if recorder.RecordToolResultMetadata(lateResultInvocation("duplicate-cell", "nested"), metadata) {
		t.Fatal("a duplicate nested ID still accepted late metadata")
	}
	attached, _ := recorder.AttachPendingToPrompt(history)
	calls := attachedCallsForItem(t, attached[1])
	if len(calls) != 1 || callResultMetadata(calls[0]) != nil {
		t.Fatalf("duplicate nested ID kept provisional metadata: %#v", calls)
	}
}
