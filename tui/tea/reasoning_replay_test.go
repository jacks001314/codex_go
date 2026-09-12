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
