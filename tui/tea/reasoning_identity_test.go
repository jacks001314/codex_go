package tea

import (
	"testing"

	"codex_go/protocol"
	codextui "codex_go/tui"
)

func reasoningTestModel(t *testing.T) *Model {
	t.Helper()
	state := codextui.NewState(nil)
	state.ThreadID = "thread-a"
	model := NewModel(state, Options{Width: 80, Height: 12})
	model.Update(ThreadEventMsg{Event: protocol.TurnStarted()})
	return model
}

func reasoningItem(id string) protocol.ThreadItem {
	return protocol.ThreadItem{ID: id, Type: "reasoning"}
}

// Mirrors Rust #43921: only the active reasoning item moves the live heading,
// and starting a new item re-selects it while retaining the previous heading
// until the new item produces a usable summary line.
func TestReasoningIdentityGatesHeadingUpdatesLikeRust(t *testing.T) {
	model := reasoningTestModel(t)
	model.Update(ThreadEventMsg{Event: protocol.ItemStarted(reasoningItem("reasoning-1"))})
	model.Update(ThreadEventMsg{Event: protocol.ReasoningSummaryDelta("reasoning-1", "## Step one\n")})
	if model.workingStatusHeader != "Step one" {
		t.Fatalf("heading = %q, want Step one", model.workingStatusHeader)
	}

	// A delta for another item must not move the heading.
	model.Update(ThreadEventMsg{Event: protocol.ReasoningSummaryDelta("reasoning-2", "other item")})
	if model.workingStatusHeader != "Step one" {
		t.Fatalf("heading = %q, want unrelated reasoning ignored", model.workingStatusHeader)
	}

	// A new item start re-selects the active identity.
	model.Update(ThreadEventMsg{Event: protocol.ItemStarted(reasoningItem("reasoning-2"))})
	if model.reasoningItemID != "reasoning-2" {
		t.Fatalf("active item = %q, want reasoning-2", model.reasoningItemID)
	}
	if model.workingStatusHeader != "Step one" {
		t.Fatalf("heading = %q, want previous heading retained", model.workingStatusHeader)
	}
	model.Update(ThreadEventMsg{Event: protocol.ReasoningSummaryDelta("reasoning-2", "## Step two")})
	if model.workingStatusHeader != "Step two" {
		t.Fatalf("heading = %q, want Step two", model.workingStatusHeader)
	}
}

// Mirrors Rust #43921: finalizing a reasoning item clears the active identity
// but keeps the last useful heading through later tool activity.
func TestReasoningFinalizeKeepsHeadingLikeRust(t *testing.T) {
	model := reasoningTestModel(t)
	model.Update(ThreadEventMsg{Event: protocol.ItemStarted(reasoningItem("reasoning-1"))})
	model.Update(ThreadEventMsg{Event: protocol.ReasoningSummaryDelta("reasoning-1", "## Step one")})
	model.Update(ThreadEventMsg{Event: protocol.ItemCompleted(reasoningItem("reasoning-1"))})
	if model.reasoningItemID != "" {
		t.Fatalf("active item = %q, want cleared", model.reasoningItemID)
	}
	if model.workingStatusHeader != "Step one" {
		t.Fatalf("heading = %q, want retained after finalize", model.workingStatusHeader)
	}
}

// Mirrors Rust #43921's restore guards: the heading is held while a
// safety-buffering wait or an active compaction owns the status row.
func TestReasoningHeadingHeldWhileStatusOwnerActiveLikeRust(t *testing.T) {
	model := reasoningTestModel(t)
	model.Update(ThreadEventMsg{Event: protocol.ItemStarted(reasoningItem("reasoning-1"))})

	model.safetyBuffering.ActiveTurnID = "turn-1"
	model.Update(ThreadEventMsg{Event: protocol.ReasoningSummaryDelta("reasoning-1", "## Hidden")})
	if model.workingStatusHeader != "" {
		t.Fatalf("heading = %q, want held while safety buffering waits", model.workingStatusHeader)
	}

	model.safetyBuffering.Clear()
	model.Update(ThreadEventMsg{Event: protocol.ReasoningSummaryDelta("reasoning-1", "\n## Visible")})
	if model.workingStatusHeader != "Visible" {
		t.Fatalf("heading = %q, want Visible after the wait clears", model.workingStatusHeader)
	}

	model.compactionActive = true
	model.Update(ThreadEventMsg{Event: protocol.ReasoningSummaryDelta("reasoning-1", "\n## Hidden again")})
	if model.workingStatusHeader != "Visible" {
		t.Fatalf("heading = %q, want held while compacting", model.workingStatusHeader)
	}
}

// Mirrors Rust #43921's resume restore: the resumed in-progress turn seeds the
// active reasoning identity so its deltas keep driving the heading while
// unrelated reasoning stays ignored.
func TestResumeSeedsActiveReasoningIdentityLikeRust(t *testing.T) {
	state := codextui.NewState(nil)
	model := NewModel(state, Options{Width: 80, Height: 12})
	model.applyResumeResponse("thread-a", SessionResumeResponse{
		Status:                 "running",
		WorkingStatusHeader:    "Step one",
		WorkingReasoningTurnID: "turn-1",
		WorkingReasoningItemID: "reasoning-9",
	})
	if model.reasoningItemID != "reasoning-9" || model.reasoningResumeTurnID != "turn-1" {
		t.Fatalf("identity = %q/%q, want reasoning-9/turn-1", model.reasoningItemID, model.reasoningResumeTurnID)
	}
	model.Update(ThreadEventMsg{Event: protocol.ReasoningSummaryDelta("reasoning-9", "\n## Step two")})
	if model.workingStatusHeader != "Step two" {
		t.Fatalf("heading = %q, want Step two", model.workingStatusHeader)
	}
	model.Update(ThreadEventMsg{Event: protocol.ReasoningSummaryDelta("other-item", "nope")})
	if model.workingStatusHeader != "Step two" {
		t.Fatalf("heading = %q, want unrelated reasoning ignored", model.workingStatusHeader)
	}
}
