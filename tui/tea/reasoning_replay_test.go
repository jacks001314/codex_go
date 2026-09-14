package tea

import (
	"strings"
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

// TestReasoningBlockIsTranscriptOnly pins Rust on_agent_reasoning_final ->
// new_reasoning_summary_block: a completed reasoning item becomes a
// transcript-only block. The live scrollback never renders it, the expanded
// transcript does, and a restored entry for the same item (Rust ReasoningReplay
// restores a switched-to thread's active item) is replaced rather than
// duplicated when the item completes.
func TestReasoningBlockIsTranscriptOnly(t *testing.T) {
	state := codextui.NewState(nil)
	state.SetThreadID("thread-1")
	model := NewModel(state, Options{Width: 80, Height: 24})

	// A switched-to thread restored the active item's snapshot as a
	// transcript-only entry carrying its item id.
	state.Messages = append(state.Messages, codextui.Message{
		Role:           codextui.RoleHistory,
		Text:           "partial",
		RawText:        "partial",
		TranscriptOnly: true,
		ItemID:         "reasoning-1",
	})

	model.Update(ThreadEventMsg{Event: protocol.ItemCompleted(protocol.ThreadItem{
		ID:      "reasoning-1",
		Type:    "reasoning",
		Summary: []string{"**Step one**\n\nThe body."},
	})})

	blocks := 0
	for _, message := range model.State.Messages {
		if !message.TranscriptOnly {
			continue
		}
		blocks++
		if message.ItemID != "reasoning-1" || message.Text != "The body." {
			t.Fatalf("reasoning block = %#v", message)
		}
	}
	if blocks != 1 {
		t.Fatalf("reasoning blocks = %d, want 1 (the restored entry is replaced)", blocks)
	}

	main := renderTranscript(state, false, 80, model.activeTUITheme())
	if strings.Contains(main, "The body.") {
		t.Fatalf("reasoning block leaked into the live scrollback:\n%s", main)
	}
	expanded := renderTranscriptWithHistoryMode(state, false, 80, model.activeTUITheme(), true)
	if !strings.Contains(expanded, "The body.") {
		t.Fatalf("expanded transcript missing the reasoning block:\n%s", expanded)
	}
}

// TestReasoningBlockCommittedWithoutRestoredEntry pins the live path: an item
// with no restored snapshot still commits its transcript-only block.
func TestReasoningBlockCommittedWithoutRestoredEntry(t *testing.T) {
	state := codextui.NewState(nil)
	state.SetThreadID("thread-1")
	model := NewModel(state, Options{Width: 80, Height: 24})

	model.Update(ThreadEventMsg{Event: protocol.ItemCompleted(protocol.ThreadItem{
		ID:      "reasoning-2",
		Type:    "reasoning",
		Summary: []string{"**Step two**\n\nSecond body."},
	})})

	found := false
	for _, message := range model.State.Messages {
		if message.TranscriptOnly && message.ItemID == "reasoning-2" && message.Text == "Second body." {
			found = true
		}
	}
	if !found {
		t.Fatalf("completed reasoning block missing: %#v", model.State.Messages)
	}
	expanded := renderTranscriptWithHistoryMode(state, false, 80, model.activeTUITheme(), true)
	if !strings.Contains(expanded, "Second body.") {
		t.Fatalf("expanded transcript missing the live reasoning block:\n%s", expanded)
	}
}
