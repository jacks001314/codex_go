package tea

import (
	"strings"
	"testing"

	"codex_go/protocol"
	codextui "codex_go/tui"
)

// TestPartialAnswerKeepsWorkingStatusLikeRust mirrors Rust #51241's snapshot test
// `answer_phase_controls_working_status_snapshot`
// (codex-rs/tui/src/chatwidget/tests/status_and_layout.rs): a partial answer
// keeps the Working indicator visible and stays a mid-turn message, while a
// final answer is the turn's answer.
//
// Rust commits `MessagePhase::PartialAnswer` with `MessagePhase::Commentary`
// (chatwidget/streaming.rs `Commentary | PartialAnswer => true`, and
// thread-store/thread_history.rs keeps both out of the final-answer summary).
// The Go TUI derives the working indicator from the turn status rather than the
// per-item status-state restore, so the phase separation it must honour is the
// message's identity: a partial answer closes the streamed answer identity
// (like commentary) and is never the turn's final answer.
func TestPartialAnswerKeepsWorkingStatusLikeRust(t *testing.T) {
	cases := []struct {
		name                string
		phase               string
		wantFinalAnswerSeen bool
		wantStreamClosed    bool
	}{
		{
			name:                "partial answer keeps the Working indicator and stays mid-turn",
			phase:               messagePhasePartialAnswer,
			wantFinalAnswerSeen: false,
			wantStreamClosed:    true,
		},
		{
			name:                "commentary keeps the Working indicator and stays mid-turn",
			phase:               messagePhaseCommentary,
			wantFinalAnswerSeen: false,
			wantStreamClosed:    true,
		},
		{
			name:                "final answer is the turn's answer",
			phase:               messagePhaseFinalAnswer,
			wantFinalAnswerSeen: true,
			wantStreamClosed:    false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := NewModel(codextui.NewState(nil), Options{})
			m.applyThreadEvent(protocol.ThreadEvent{Type: "turn.started", ThreadID: "thread-1", TurnID: "turn-1"})
			if !m.isTaskRunning() {
				t.Fatalf("turn did not start running: status = %q", m.State.Status)
			}
			m.appendAssistantDelta("msg-partial", "Partial text.")
			if m.Transcript.activeAssistantDeltaItemID != "msg-partial" {
				t.Fatalf("streamed answer identity = %q, want msg-partial", m.Transcript.activeAssistantDeltaItemID)
			}

			updated, _ := m.Update(ThreadEventMsg{Event: protocol.ThreadEvent{
				Type: "item.completed",
				Item: &protocol.ThreadItem{ID: "msg-partial", Type: "agent_message", Text: "Partial text.", Phase: tc.phase},
			}})
			m = updated.(*Model)

			if got := m.turnFinalAnswerSeen; got != tc.wantFinalAnswerSeen {
				t.Fatalf("turnFinalAnswerSeen = %v, want %v", got, tc.wantFinalAnswerSeen)
			}
			closed := m.Transcript.activeAssistantDeltaItemID == ""
			if closed != tc.wantStreamClosed {
				t.Fatalf("stream identity closed = %v (id %q), want %v", closed, m.Transcript.activeAssistantDeltaItemID, tc.wantStreamClosed)
			}
			// The text is committed to the transcript either way.
			if len(m.State.Messages) == 0 || !strings.Contains(m.State.Messages[len(m.State.Messages)-1].Text, "Partial text.") {
				t.Fatalf("messages = %#v, want the completed message", m.State.Messages)
			}
			if !m.isTaskRunning() {
				t.Fatalf("turn stopped running on %q: status = %q", tc.phase, m.State.Status)
			}
			if tc.wantFinalAnswerSeen {
				return
			}
			// Rust's snapshot expects `• Working (0s • esc to interrupt)` to stay
			// on screen after a partial answer.
			if indicator := m.renderWorkingIndicator(); indicator == "" || !strings.Contains(indicator, "Working") {
				t.Fatalf("working indicator after %q = %q, want a visible Working row", tc.phase, indicator)
			}
		})
	}
}
