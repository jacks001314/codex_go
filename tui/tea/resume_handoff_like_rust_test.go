package tea

import (
	"strings"
	"testing"

	"codex_go/protocol"
	codextui "codex_go/tui"
)

// Rust #61216 drains the outgoing thread's events before a resume installs the
// next session. A notification that was already queued must not switch the
// view back to the conversation the user just left.
func TestResumeHandoffDropsLateOutgoingThreadEventsLikeRust(t *testing.T) {
	model := NewModel(codextui.NewState(nil), Options{Width: 120, Height: 40})
	model.State.SetThreadID("thread-old")

	model.applyResumeResponseWithCWD("thread-new", SessionResumeResponse{
		Summary: &codextui.SessionSummary{ThreadID: "thread-new", Title: "new"},
	}, "/repo")
	if got := model.currentThreadID(); got != "thread-new" {
		t.Fatalf("resumed thread = %q", got)
	}
	if model.outgoingThreadID != "thread-old" {
		t.Fatalf("outgoing thread = %q, want thread-old", model.outgoingThreadID)
	}
	model.setStatus("running")
	model.currentTurnID = "turn-old"

	// A stale thread/started from the outgoing stream must not switch back.
	model.Update(ThreadEventMsg{Event: protocol.ThreadStarted("thread-old")})
	if got := model.currentThreadID(); got != "thread-new" {
		t.Fatalf("late thread/started switched view to %q", got)
	}

	lateTurn := protocol.TurnCompletedWithStatus(protocol.Usage{}, "turn-old", false)
	lateTurn.ThreadID = "thread-old"
	model.Update(ThreadEventMsg{Event: lateTurn})
	if model.State.Status != "running" {
		t.Fatalf("late outgoing turn completed the new thread: status=%q", model.State.Status)
	}

	lateItem := protocol.ItemCompleted(protocol.AgentMessageItem("late-1", "late output"))
	lateItem.ThreadID = "thread-old"
	model.Update(ThreadEventMsg{Event: lateItem})
	if strings.Contains(model.transcriptContent(), "late output") {
		t.Fatal("late outgoing item painted the new thread")
	}

	// The live thread's own notifications still apply.
	liveItem := protocol.ItemCompleted(protocol.AgentMessageItem("live-1", "live output"))
	liveItem.ThreadID = "thread-new"
	model.Update(ThreadEventMsg{Event: liveItem})
	if !strings.Contains(model.transcriptContent(), "live output") {
		t.Fatal("live thread item was dropped")
	}

	// Attaching the previously replaced thread makes it live again.
	model.applyResumeResponseWithCWD("thread-old", SessionResumeResponse{
		Summary: &codextui.SessionSummary{ThreadID: "thread-old", Title: "old"},
	}, "/repo")
	if model.outgoingThreadID != "thread-new" {
		t.Fatalf("outgoing thread after returning = %q, want thread-new", model.outgoingThreadID)
	}
	back := protocol.ItemCompleted(protocol.AgentMessageItem("back-1", "back output"))
	back.ThreadID = "thread-old"
	model.Update(ThreadEventMsg{Event: back})
	if !strings.Contains(model.transcriptContent(), "back output") {
		t.Fatal("reattached thread item was dropped")
	}
}
