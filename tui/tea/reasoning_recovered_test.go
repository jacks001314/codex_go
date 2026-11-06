package tea

import (
	"testing"

	"codex_go/protocol"
	codextui "codex_go/tui"
)

// TestModelReasoningCompletionReplacesStreamedSummaryLikeRust covers Rust
// #59698: a completed reasoning item's parts replace a partial stream instead
// of being appended after it.
func TestModelReasoningCompletionReplacesStreamedSummaryLikeRust(t *testing.T) {
	state := codextui.NewState(nil)
	state.SetStatus("running")
	model := NewModel(state, Options{Width: 80, Height: 24})
	model.Update(ThreadEventMsg{Event: protocol.ReasoningSummaryDelta("reasoning-1", "## Partial")})
	model.Update(ThreadEventMsg{Event: protocol.ItemCompleted(protocol.ThreadItem{
		ID:      "reasoning-1",
		Type:    "reasoning",
		Summary: []string{"## Complete summary"},
	})})
	if model.workingStatusHeader != "Complete summary" {
		t.Fatalf("heading = %q, want the completed item's summary", model.workingStatusHeader)
	}
	if model.reasoningItemID != "" {
		t.Fatalf("active reasoning item = %q, want finalized", model.reasoningItemID)
	}
	found := false
	for _, message := range model.State.Messages {
		if message.TranscriptOnly && message.ItemID == "reasoning-1" {
			found = true
			if message.Text != "## Complete summary" {
				t.Fatalf("reasoning block = %q, want complete summary", message.Text)
			}
		}
	}
	if !found {
		t.Fatalf("completed reasoning block missing: %#v", model.State.Messages)
	}
}

// TestModelReasoningEmptyCompletionPreservesStreamedSummaryLikeRust pins the
// empty-completion guard in replace_reasoning_parts.
func TestModelReasoningEmptyCompletionPreservesStreamedSummaryLikeRust(t *testing.T) {
	state := codextui.NewState(nil)
	state.SetStatus("running")
	model := NewModel(state, Options{Width: 80, Height: 24})
	model.Update(ThreadEventMsg{Event: protocol.ReasoningSummaryDelta("reasoning-1", "## Streamed")})
	model.Update(ThreadEventMsg{Event: protocol.ItemCompleted(protocol.ThreadItem{
		ID:      "reasoning-1",
		Type:    "reasoning",
		Summary: []string{},
	})})
	if model.workingStatusHeader != "Streamed" {
		t.Fatalf("heading = %q, want the streamed summary retained", model.workingStatusHeader)
	}
}

// TestModelReasoningItemStartedReplacesPartsLikeRust covers summary parts that
// arrive on item/started and the raw-content visibility gate.
func TestModelReasoningItemStartedReplacesPartsLikeRust(t *testing.T) {
	state := codextui.NewState(nil)
	state.SetStatus("running")
	model := NewModel(state, Options{Width: 80, Height: 24, ShowRawReasoning: true})
	model.Update(ThreadEventMsg{Event: protocol.ItemStarted(protocol.ThreadItem{
		ID:      "reasoning-1",
		Type:    "reasoning",
		Summary: []string{"## Snapshot summary"},
		Content: []string{"## Raw reasoning"},
	})})
	if model.reasoningItemID != "reasoning-1" {
		t.Fatalf("active reasoning item = %q", model.reasoningItemID)
	}
	if model.workingStatusHeader != "Raw reasoning" {
		t.Fatalf("raw-visible heading = %q, want the complete raw part", model.workingStatusHeader)
	}
}
