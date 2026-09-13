package tea

import (
	"testing"

	"codex_go/protocol"
	codextui "codex_go/tui"
)

// TestModelSwitchRestoresBufferedActiveReasoning covers Rust #43921's
// ReasoningReplay boundary: when the read snapshot identifies no active
// reasoning item, the buffered stream restores it and seeds the heading.
func TestModelSwitchRestoresBufferedActiveReasoning(t *testing.T) {
	state := codextui.NewState(nil)
	state.SetThreadID("thread-1")
	model := NewModel(state, Options{Width: 80, Height: 24})
	model.backgroundThreadEvents["thread-2"] = []protocol.ThreadEvent{
		protocol.ItemStarted(protocol.ThreadItem{ID: "call-1", Type: "command_execution"}),
		protocol.ReasoningSummaryDelta("reasoning-1", "## Buffered heading"),
		protocol.ReasoningSummaryDelta("reasoning-1", "\nmore detail"),
	}

	model.Update(AgentSwitchResultMsg{
		ThreadID: "thread-2",
		Response: AgentThreadSwitchResponse{
			Entry:  codextui.AgentThreadEntry{ThreadID: "thread-2", AgentNickname: "agent"},
			Status: "running",
		},
	})
	if model.reasoningItemID != "reasoning-1" {
		t.Fatalf("active reasoning item = %q, want reasoning-1", model.reasoningItemID)
	}
	if model.workingStatusHeader != "more detail" {
		t.Fatalf("heading = %q, want the latest buffered summary line", model.workingStatusHeader)
	}
	if len(model.backgroundThreadEvents) != 0 {
		t.Fatalf("buffer was not consumed: %#v", model.backgroundThreadEvents)
	}
}

// TestModelSwitchPrefersTheSnapshotReasoningItem covers the priority: a
// snapshot-identified active item is authoritative over buffered deltas.
func TestModelSwitchPrefersTheSnapshotReasoningItem(t *testing.T) {
	state := codextui.NewState(nil)
	state.SetThreadID("thread-1")
	model := NewModel(state, Options{Width: 80, Height: 24})
	model.backgroundThreadEvents["thread-2"] = []protocol.ThreadEvent{
		protocol.ReasoningSummaryDelta("stale-item", "## Stale"),
	}
	model.Update(AgentSwitchResultMsg{
		ThreadID: "thread-2",
		Response: AgentThreadSwitchResponse{
			Entry:                  codextui.AgentThreadEntry{ThreadID: "thread-2", AgentNickname: "agent"},
			Status:                 "running",
			WorkingStatusHeader:    "Snapshot heading",
			WorkingReasoningItemID: "snapshot-item",
		},
	})
	if model.reasoningItemID != "snapshot-item" || model.workingStatusHeader != "Snapshot heading" {
		t.Fatalf("reasoning state = (item=%q, heading=%q)", model.reasoningItemID, model.workingStatusHeader)
	}
}

// TestModelSwitchDropsCompletedBufferedReasoning covers the store's marker
// lifecycle (Rust ThreadEventStore.active_reasoning_item): a buffered reasoning
// item that completed is not the live item, so a switch must not revive it.
func TestModelSwitchDropsCompletedBufferedReasoning(t *testing.T) {
	state := codextui.NewState(nil)
	state.SetThreadID("thread-1")
	model := NewModel(state, Options{Width: 80, Height: 24})
	model.backgroundThreadEvents["thread-2"] = []protocol.ThreadEvent{
		protocol.ItemStarted(protocol.ThreadItem{ID: "reasoning-1", Type: "reasoning"}),
		protocol.ReasoningSummaryDelta("reasoning-1", "## Finished heading"),
		protocol.ItemCompleted(protocol.ThreadItem{ID: "reasoning-1", Type: "reasoning"}),
	}

	model.Update(AgentSwitchResultMsg{
		ThreadID: "thread-2",
		Response: AgentThreadSwitchResponse{
			Entry:  codextui.AgentThreadEntry{ThreadID: "thread-2", AgentNickname: "agent"},
			Status: "running",
		},
	})
	if model.reasoningItemID != "" {
		t.Fatalf("active reasoning item = %q, want none for a completed item", model.reasoningItemID)
	}
	if model.workingStatusHeader == "Finished heading" {
		t.Fatalf("heading = %q, want the completed item's snapshot not to seed it", model.workingStatusHeader)
	}
}

// TestModelSwitchTracksLatestBufferedReasoningMarker covers the marker moving
// to the newest started reasoning item after an earlier one completed.
func TestModelSwitchTracksLatestBufferedReasoningMarker(t *testing.T) {
	state := codextui.NewState(nil)
	state.SetThreadID("thread-1")
	model := NewModel(state, Options{Width: 80, Height: 24})
	model.backgroundThreadEvents["thread-2"] = []protocol.ThreadEvent{
		protocol.ItemStarted(protocol.ThreadItem{ID: "reasoning-1", Type: "reasoning"}),
		protocol.ReasoningSummaryDelta("reasoning-1", "## Earlier heading"),
		protocol.ItemCompleted(protocol.ThreadItem{ID: "reasoning-1", Type: "reasoning"}),
		protocol.ItemStarted(protocol.ThreadItem{ID: "reasoning-2", Type: "reasoning"}),
		protocol.ReasoningSummaryDelta("reasoning-2", "## Latest heading"),
	}

	model.Update(AgentSwitchResultMsg{
		ThreadID: "thread-2",
		Response: AgentThreadSwitchResponse{
			Entry:  codextui.AgentThreadEntry{ThreadID: "thread-2", AgentNickname: "agent"},
			Status: "running",
		},
	})
	if model.reasoningItemID != "reasoning-2" {
		t.Fatalf("active reasoning item = %q, want reasoning-2", model.reasoningItemID)
	}
	if model.workingStatusHeader != "Latest heading" {
		t.Fatalf("heading = %q, want the latest buffered summary line", model.workingStatusHeader)
	}
}

// A buffered turn boundary clears the marker: the completed turn's reasoning
// item no longer owns later deltas.
func TestModelSwitchClearsBufferedReasoningOnTurnBoundary(t *testing.T) {
	state := codextui.NewState(nil)
	state.SetThreadID("thread-1")
	model := NewModel(state, Options{Width: 80, Height: 24})
	model.backgroundThreadEvents["thread-2"] = []protocol.ThreadEvent{
		protocol.ItemStarted(protocol.ThreadItem{ID: "reasoning-1", Type: "reasoning"}),
		protocol.ReasoningSummaryDelta("reasoning-1", "## Old turn heading"),
		protocol.TurnCompleted(protocol.Usage{}),
	}

	model.Update(AgentSwitchResultMsg{
		ThreadID: "thread-2",
		Response: AgentThreadSwitchResponse{
			Entry:  codextui.AgentThreadEntry{ThreadID: "thread-2", AgentNickname: "agent"},
			Status: "running",
		},
	})
	if model.reasoningItemID != "" {
		t.Fatalf("active reasoning item = %q, want none after the turn ended", model.reasoningItemID)
	}
}
