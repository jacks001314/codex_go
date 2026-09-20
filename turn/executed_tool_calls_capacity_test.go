package turn

import (
	"strconv"
	"testing"

	"codex_go/model"
)

// assertCompleteCodeModeCellAttachment attaches one finished Code Mode cell's
// exec input/output pair and asserts the complete inventory, executed call, cell
// id and completion marker Rust #46712 restores under recorder pressure.
func assertCompleteCodeModeCellAttachment(t *testing.T, recorder *ExecutedToolCallRecorder, cellID string, outputCallID string, message string) {
	t.Helper()
	items := []any{
		codeModeExecInputItem(outputCallID),
		&ToolResponseItem{Type: "custom_tool_call_output", CallID: outputCallID, Output: NewFunctionCallOutputPayload(message, nil)},
	}
	attached, token := recorder.AttachPendingToPrompt(items)
	if token == nil {
		t.Fatalf("cell %s produced no attachment", cellID)
	}
	object := marshalExecutedToolCallItem(t, model.BoundExecutedToolCallsForPrompt(attached)[len(attached)-1])
	metadata, ok := object["internal_chat_message_metadata_passthrough"].(map[string]any)
	if !ok {
		t.Fatalf("cell %s attached no metadata: %#v", cellID, object)
	}
	if metadata["cell_id"] != cellID {
		t.Fatalf("cell %s cell_id = %#v, want %q", cellID, metadata["cell_id"], cellID)
	}
	if metadata["tool_calls_complete"] != true {
		t.Fatalf("cell %s tool_calls_complete = %#v, want true", cellID, metadata["tool_calls_complete"])
	}
	calls := executedToolCallsFromObject(t, object)
	if len(calls) != 1 || calls[0]["name"] != "mcp__echo" {
		t.Fatalf("cell %s calls = %#v", cellID, calls)
	}
	recorder.CommitAttachment(token)
}

// Mirrors Rust #46712's mapping-pressure reclamation: an ended cell can leave
// output mappings behind with nothing left to attach, and those mappings must
// not consume the budget a fresh Code Mode call needs.
func TestExecutedToolCallRecorderReclaimsOrphanedMappingsLikeRust(t *testing.T) {
	recorder := NewExecutedToolCallRecorder()
	recorder.RegisterCell("live", "live-output")
	// Go's lifecycle removes a mapping together with its group, so the orphaned
	// state Rust reclaims - a cell dropped while its output mappings remain - is
	// built directly here.
	for index := 0; index < maxPendingExecutedToolCalls-1; index++ {
		recorder.outputs["orphan-"+strconv.Itoa(index)] = "cell:orphan"
	}
	if got := len(recorder.outputs); got != maxPendingExecutedToolCalls {
		t.Fatalf("output mappings = %d, want %d", got, maxPendingExecutedToolCalls)
	}

	recorder.RegisterCell("fresh", "fresh-output")
	if _, ok := recorder.outputs["fresh-output"]; !ok {
		t.Fatal("fresh cell registration was refused under orphaned mapping pressure")
	}
	recorder.RecordToolCall(codeModeNestedInvocation("fresh-call", "fresh", "recovered"), model.ToolModeCodeMode)
	recorder.FinishCell("fresh")

	assertCompleteCodeModeCellAttachment(t, recorder, "fresh", "fresh-output", "recovered")
}

// Mirrors Rust #46712's capacity-pressure recovery: finished cells that no
// output mapping can reach are evicted - revoking their completeness and
// releasing their pending-call budget - while a reachable cell keeps its
// evidence and the cell being registered is never discarded.
func TestExecutedToolCallRecorderEvictsUnreachableFinishedGroupsLikeRust(t *testing.T) {
	recorder := NewExecutedToolCallRecorder()
	// The first cell keeps its output mapping, so its recorded call stays
	// attachable and it must never be chosen as the victim.
	recorder.RegisterCell("kept", "kept-output")
	recorder.RecordToolCall(codeModeNestedInvocation("kept-call", "kept", "kept"), model.ToolModeCodeMode)
	recorder.FinishCell("kept")
	// The remaining finished cells hold the whole pending-call budget without
	// any output that could attach their records.
	for index := 0; index < maxPendingExecutedToolCalls-1; index++ {
		cellID := "stale-" + strconv.Itoa(index)
		recorder.RecordToolCall(codeModeNestedInvocation("stale-call-"+strconv.Itoa(index), cellID, "stale"), model.ToolModeCodeMode)
		recorder.FinishCell(cellID)
	}
	if got := recorder.pendingNestedCalls(); got != maxPendingExecutedToolCalls {
		t.Fatalf("pending nested calls = %d, want %d", got, maxPendingExecutedToolCalls)
	}

	recorder.RegisterCell("fresh", "fresh-output")
	if recorder.groups["cell:fresh"] == nil {
		t.Fatal("fresh cell registration was refused under pending-call pressure")
	}
	recorder.RecordToolCall(codeModeNestedInvocation("fresh-call", "fresh", "recovered"), model.ToolModeCodeMode)
	recorder.FinishCell("fresh")

	// The lowest unreachable finished cell released its pending record, and its
	// completeness cannot come back.
	if recorder.groups["cell:stale-0"] != nil {
		t.Fatal("the eviction did not release the stale cell's pending-call budget")
	}
	if !recorder.groupInvalid("cell:stale-0") {
		t.Fatal("the evicted cell kept its completeness")
	}
	if recorder.groups["cell:kept"] == nil {
		t.Fatal("the eviction discarded a cell an output mapping can still attach")
	}

	assertCompleteCodeModeCellAttachment(t, recorder, "fresh", "fresh-output", "recovered")
	assertCompleteCodeModeCellAttachment(t, recorder, "kept", "kept-output", "kept")
}
