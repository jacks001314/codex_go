package realtime

import "testing"

// Rust #62288: a handoff attributes the transcript frames it carries, so each
// utterance keeps the realtime frame it came from instead of the whole event's
// id. A delta and its final are one frame.
func TestRealtimeTranscriptEntriesCarryFrameIdentityLikeRust(t *testing.T) {
	session := newV3TranscriptSession()
	v3Event(session, Event{Type: "input_transcript.delta", Delta: "hello", ItemID: "frame-user"})
	v3Event(session, Event{Type: "input_transcript.done", Text: "hello there", ItemID: "frame-user"})
	v3Event(session, Event{Type: "output_transcript.delta", Delta: "hi", ItemID: "frame-assistant"})
	handoff := v3Event(session, Event{Type: "handoff.requested", InputTranscript: "hello there"})

	if len(handoff.ActiveTranscript) != 2 {
		t.Fatalf("active transcript = %#v, want two entries", handoff.ActiveTranscript)
	}
	if got := handoff.ActiveTranscript[0]; got.Role != "user" || got.Text != "hello there" || got.ID != "frame-user" {
		t.Fatalf("user entry = %#v", got)
	}
	if got := handoff.ActiveTranscript[1]; got.Role != "assistant" || got.Text != "hi" || got.ID != "frame-assistant" {
		t.Fatalf("assistant entry = %#v", got)
	}
}
