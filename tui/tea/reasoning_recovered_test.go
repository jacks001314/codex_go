package tea

import (
	"testing"

	"codex_go/protocol"
	codextui "codex_go/tui"
)

// TestModelReasoningRecoveredAfterRefreshReconcilesCompleteItem covers Rust
// #43921: a reasoning item restored after a refresh (whose earlier deltas may be
// missing) reconciles the streamed buffer with the completed item's complete
// summary before the heading is finalized.
func TestModelReasoningRecoveredAfterRefreshReconcilesCompleteItem(t *testing.T) {
	state := codextui.NewState(nil)
	state.SetStatus("running")
	model := NewModel(state, Options{Width: 80, Height: 24})

	// Simulate a switch/resume restore: the active item is known, but only a
	// partial summary was streamed.
	model.reasoningItemID = "reasoning-1"
	model.reasoningRecoveredAfterRefresh = true
	model.reasoningSummaryBuffers = map[string]string{"reasoning-1": "## Partial"}
	model.setWorkingStatusHeader("Partial")

	model.Update(ThreadEventMsg{Event: protocol.ItemCompleted(protocol.ThreadItem{
		ID:   "reasoning-1",
		Type: "reasoning",
		Text: "## Complete summary",
	})})
	if model.workingStatusHeader != "Complete summary" {
		t.Fatalf("heading = %q, want the completed item's summary", model.workingStatusHeader)
	}
	if model.reasoningRecoveredAfterRefresh {
		t.Fatal("the reconciliation flag must clear after the completed item")
	}
	if model.reasoningItemID != "" {
		t.Fatalf("active reasoning item = %q, want finalized", model.reasoningItemID)
	}
}

// TestModelReasoningCompletionWithoutRecoveryKeepsHeading covers the ordinary
// path: without a restored item the completed item does not rewrite the heading.
func TestModelReasoningCompletionWithoutRecoveryKeepsHeading(t *testing.T) {
	state := codextui.NewState(nil)
	state.SetStatus("running")
	model := NewModel(state, Options{Width: 80, Height: 24})
	model.Update(ThreadEventMsg{Event: protocol.ReasoningSummaryDelta("reasoning-1", "## Streamed")})
	if model.workingStatusHeader != "Streamed" {
		t.Fatalf("streamed heading = %q", model.workingStatusHeader)
	}
	model.Update(ThreadEventMsg{Event: protocol.ItemCompleted(protocol.ThreadItem{
		ID:   "reasoning-1",
		Type: "reasoning",
		Text: "## Complete summary",
	})})
	if model.workingStatusHeader != "Streamed" {
		t.Fatalf("heading = %q, want the streamed summary retained", model.workingStatusHeader)
	}
}
