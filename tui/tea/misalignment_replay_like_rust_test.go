package tea

import (
	"strings"
	"testing"

	"codex_go/protocol"
	codextui "codex_go/tui"
)

// Rust #42399: a buffered misalignment error from an older turn must not be
// replayed after a later turn has continued, while the latest turn's error
// remains visible.
func TestBufferedReplayOmitsResolvedMisalignmentErrorsLikeRust(t *testing.T) {
	events := []protocol.ThreadEvent{
		{Type: "error", TurnID: "turn-old", Error: &protocol.ThreadError{Message: "misalignmentPolicyViolation: old"}},
		{Type: "turn.started", TurnID: "turn-new"},
		{Type: "error", TurnID: "turn-new", Error: &protocol.ThreadError{Message: "misalignmentPolicyViolation: current"}},
	}
	filtered := filterResolvedMisalignmentReplayEvents(events)
	if len(filtered) != 2 || filtered[0].TurnID != "turn-new" || filtered[1].TurnID != "turn-new" {
		t.Fatalf("filtered events = %#v, want only the latest turn", filtered)
	}

	var messages []codextui.Message
	for _, event := range filtered {
		messages = applyBufferedThreadEventToMessages(messages, event)
	}
	joined := ""
	for _, message := range messages {
		joined += message.Text + "\n"
	}
	if strings.Contains(joined, "old") || !strings.Contains(joined, "current") {
		t.Fatalf("replayed messages = %q", joined)
	}
}

// Without a turn identity the filter must not guess and drop history.
func TestBufferedReplayKeepsUnidentifiedErrorsLikeRust(t *testing.T) {
	events := []protocol.ThreadEvent{
		{Type: "error", Error: &protocol.ThreadError{Message: "misalignmentPolicyViolation"}},
		{Type: "turn.started"},
	}
	if filtered := filterResolvedMisalignmentReplayEvents(events); len(filtered) != len(events) {
		t.Fatalf("filtered events = %#v, want unchanged", filtered)
	}
}
