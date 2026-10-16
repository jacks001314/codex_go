package appserver

import (
	"strings"
	"testing"

	"codex_go/realtime"
	"codex_go/session"
)

// Rust #54257 bounds the active realtime transcript to 8 KiB before the
// transcript is consumed (codex-api/src/endpoint/realtime_websocket/methods.rs:792-839).
// The appserver handoff and tail paths apply the same bound, so both sides of a
// handoff agree on the retained speech and an unbounded producer cannot reach
// thread history.
func TestRealtimeTranscriptTailIsBoundedLikeRust(t *testing.T) {
	router, store, _, _ := newRealtimeTailRouter(t)
	threadID := startRealtimeTailThread(t, router)

	router.handleRealtimeEvent(threadID, realtime.Event{
		Type: "transcript_tail.flush",
		ActiveTranscript: []realtime.TranscriptEntry{
			{Role: "user", Text: strings.Repeat("u", 5000)},
			{Role: "assistant", Text: strings.Repeat("a", 4000)},
			{Role: "user", Text: "Only update the tests."},
		},
	})

	record, err := store.Read(session.ThreadID(threadID), true, true)
	if err != nil {
		t.Fatalf("read realtime thread: %v", err)
	}
	var recorded *session.Item
	for index := range record.Items {
		item := &record.Items[index]
		if item.Metadata["kind"] == realtimeTranscriptTailKind {
			recorded = item
			break
		}
	}
	if recorded == nil {
		t.Fatalf("transcript tail was not persisted into thread history: %#v", record.Items)
	}
	// Untimed FIFO eviction drops the oldest utterance; the newest instruction
	// and the assistant speech that follows it survive inside the budget.
	if strings.Contains(recorded.Text, strings.Repeat("u", 5000)) {
		t.Fatalf("recorded tail kept the evicted utterance: %d bytes", len(recorded.Text))
	}
	if !strings.HasSuffix(recorded.Text, "Only update the tests.") {
		t.Fatalf("recorded tail dropped the newest instruction: %q", recorded.Text)
	}
	if len(recorded.Text) > 8*1024 {
		t.Fatalf("recorded tail = %d bytes, over the budget", len(recorded.Text))
	}
}
