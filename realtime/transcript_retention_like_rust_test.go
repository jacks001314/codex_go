package realtime

import (
	"strings"
	"testing"
)

func newV3TranscriptSession() *realtimeTransportSession {
	return &realtimeTransportSession{config: SessionConfig{Version: VersionV3}}
}

func v3Event(session *realtimeTransportSession, event Event) Event {
	updated, _, _, _ := session.updateTranscript(event)
	return updated
}

// TestRealtimeV3HandoffRetainsBothSpeakersWithoutTruncationLikeRust mirrors
// Rust #58221: the fixed active-transcript byte budget is gone, so a handoff
// keeps both speakers' complete utterances, in conversation order.
func TestRealtimeV3HandoffRetainsBothSpeakersWithoutTruncationLikeRust(t *testing.T) {
	oldUser := strings.Repeat("u", 4000)
	assistant := strings.Repeat("a", 4000)
	newestUser := strings.Repeat("n", 190) + "Only update the tests."
	assistantSuffix := strings.Repeat("b", 211)
	assistantFinal := assistant + assistantSuffix
	user := oldUser + newestUser

	session := newV3TranscriptSession()
	v3Event(session, Event{Type: "input_transcript.delta", Delta: oldUser})
	v3Event(session, Event{Type: "output_transcript.delta", Delta: assistant})
	v3Event(session, Event{Type: "input_transcript.delta", Delta: newestUser})
	v3Event(session, Event{Type: "output_transcript.delta", Delta: assistantSuffix})
	if completed := v3Event(session, Event{Type: "output_transcript.done", Text: assistantFinal}).CompletedTranscript; len(completed) != 0 {
		t.Fatalf("completed transcript = %#v, want none while the user utterance is still open", completed)
	}
	handoff := v3Event(session, Event{Type: "handoff.requested", InputTranscript: user})

	want := []TranscriptEntry{
		{Role: "user", Text: user},
		{Role: "assistant", Text: assistantFinal},
	}
	if len(handoff.ActiveTranscript) != len(want) {
		t.Fatalf("active transcript = %#v, want %#v", handoff.ActiveTranscript, want)
	}
	for index := range want {
		if handoff.ActiveTranscript[index] != want[index] {
			t.Fatalf("active transcript[%d] = %#v, want %#v", index, handoff.ActiveTranscript[index], want[index])
		}
	}
}

// TestRealtimeV3CompletedTranscriptFlushesBeforeOpenUtteranceLikeRust mirrors
// Rust #58221 take_completed_transcript: turn.done flushes everything before
// the other speaker's still-open utterance, and an echoed handoff input that
// was already flushed is not recorded twice.
func TestRealtimeV3CompletedTranscriptFlushesBeforeOpenUtteranceLikeRust(t *testing.T) {
	oldUser := strings.Repeat("u", 4000)
	assistant := strings.Repeat("a", 4000)
	newestUser := "Only update the tests."

	session := newV3TranscriptSession()
	v3Event(session, Event{Type: "input_audio_buffer.speech_started"})
	v3Event(session, Event{Type: "input_transcript.delta", Delta: oldUser})
	v3Event(session, Event{Type: "output_transcript.delta", Delta: assistant})
	v3Event(session, Event{Type: "input_audio_buffer.speech_started"})
	v3Event(session, Event{Type: "input_transcript.delta", Delta: newestUser})

	done := v3Event(session, Event{Type: "output_transcript.done", Text: assistant})
	if len(done.CompletedTranscript) != 2 ||
		done.CompletedTranscript[0] != (TranscriptEntry{Role: "user", Text: oldUser}) ||
		done.CompletedTranscript[1] != (TranscriptEntry{Role: "assistant", Text: assistant}) {
		t.Fatalf("completed transcript = %#v", done.CompletedTranscript)
	}

	// The echoed handoff input matches the flushed user utterance, so it must
	// not be appended again; the open utterance stays in the active transcript.
	handoff := v3Event(session, Event{Type: "handoff.requested", InputTranscript: oldUser})
	if len(handoff.ActiveTranscript) != 1 || handoff.ActiveTranscript[0] != (TranscriptEntry{Role: "user", Text: newestUser}) {
		t.Fatalf("active transcript after flushed handoff = %#v", handoff.ActiveTranscript)
	}
}

// TestRealtimeV3TranscriptTailIsNotTruncatedLikeRust mirrors Rust #58221: the
// terminal transcript tail keeps every utterance instead of an 8 KiB suffix.
func TestRealtimeV3TranscriptTailIsNotTruncatedLikeRust(t *testing.T) {
	user := strings.Repeat("u", 5000)
	assistant := strings.Repeat("a", 4000)
	session := newV3TranscriptSession()
	session.config.FlushTranscriptTailOnEnd = true
	v3Event(session, Event{Type: "input_transcript.delta", Delta: user})
	v3Event(session, Event{Type: "output_transcript.delta", Delta: assistant})
	tail := session.takeTranscriptTail()
	if len(tail) != 2 || tail[0].Text != user || tail[1].Text != assistant {
		t.Fatalf("transcript tail = %#v", tail)
	}
	if total := realtimeTranscriptEntriesBytes(tail); total <= 8*1024 {
		t.Fatalf("tail bytes = %d, expected the full untruncated transcript", total)
	}
}
