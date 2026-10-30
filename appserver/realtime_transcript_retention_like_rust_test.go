package appserver

import (
	"strings"
	"testing"

	"codex_go/realtime"
	"codex_go/session"
)

func recordedRealtimeItem(t *testing.T, store *session.Store, threadID string, kind string) *session.Item {
	t.Helper()
	record, err := store.Read(session.ThreadID(threadID), true, true)
	if err != nil {
		t.Fatalf("read realtime thread: %v", err)
	}
	for index := range record.Items {
		item := &record.Items[index]
		if item.Metadata["kind"] == kind {
			return item
		}
	}
	return nil
}

// TestRealtimeCompletedTranscriptChunkRecordedLikeRust mirrors Rust #58221:
// a completed transcript chunk reaches thread history as a delegated user
// message before any later handoff can consume the same speech.
func TestRealtimeCompletedTranscriptChunkRecordedLikeRust(t *testing.T) {
	router, store, _, _ := newRealtimeTailRouter(t)
	threadID := startRealtimeTailThread(t, router)

	router.handleRealtimeEvent(threadID, realtime.Event{
		Type: "output_transcript.done",
		CompletedTranscript: []realtime.TranscriptEntry{
			{Role: "user", Text: "What can you do?"},
			{Role: "assistant", Text: "I can help with code."},
		},
	})

	recorded := recordedRealtimeItem(t, store, threadID, realtimeTranscriptChunkKind)
	if recorded == nil {
		t.Fatal("completed transcript chunk was not persisted into thread history")
	}
	delta := "user: What can you do?\nassistant: I can help with code."
	want := "<realtime_delegation>\n  <input>" + delta + "</input>\n  <transcript_delta>" + delta + "</transcript_delta>\n</realtime_delegation>"
	if recorded.Text != want {
		t.Fatalf("recorded chunk = %q, want %q", recorded.Text, want)
	}
	if recorded.Metadata["source"] != "transcript_delta" {
		t.Fatalf("recorded chunk source = %#v", recorded.Metadata["source"])
	}
}

// TestRealtimeTranscriptTailKeepsFullUtterancesLikeRust mirrors Rust #58221:
// the terminal transcript tail is no longer cut down to an 8 KiB suffix.
func TestRealtimeTranscriptTailKeepsFullUtterancesLikeRust(t *testing.T) {
	router, store, _, _ := newRealtimeTailRouter(t)
	threadID := startRealtimeTailThread(t, router)

	user := strings.Repeat("u", 5000)
	assistant := strings.Repeat("a", 4000)
	router.handleRealtimeEvent(threadID, realtime.Event{
		Type: "transcript_tail.flush",
		ActiveTranscript: []realtime.TranscriptEntry{
			{Role: "user", Text: user},
			{Role: "assistant", Text: assistant},
		},
	})

	recorded := recordedRealtimeItem(t, store, threadID, realtimeTranscriptTailKind)
	if recorded == nil {
		t.Fatal("transcript tail was not persisted into thread history")
	}
	if recorded.Text != user+"\n"+assistant {
		t.Fatalf("recorded tail = %d bytes, want the full %d-byte transcript", len(recorded.Text), len(user)+len(assistant)+1)
	}
}
