package appserver

// Mirrors Rust #62288 on the Go history path: a completed transcript chunk is
// written before a handoff can expose the same speech to the model, an
// uncertain write is retried with the same input, an unconfirmed seal demands
// history recovery, and durable speech restored at reconnect precedes newly
// captured speech.

import (
	"errors"
	"strings"
	"testing"

	"codex_go/realtime"
	"codex_go/session"
)

// typedUserItems returns the user messages a thread recorded for one text.
func typedUserItems(t *testing.T, store *session.Store, threadID string, text string) []session.Item {
	t.Helper()
	record, err := store.Read(session.ThreadID(threadID), true, true)
	if err != nil {
		t.Fatalf("read thread: %v", err)
	}
	var items []session.Item
	for index := range record.Items {
		item := record.Items[index]
		if item.Role == "user" && strings.Contains(item.Text, text) {
			items = append(items, item)
		}
	}
	return items
}

func TestRealtimeTranscriptSealRetriesTheSameInputLikeRust(t *testing.T) {
	router, store, _, _ := newRealtimeTailRouter(t)
	threadID := startRealtimeTailThread(t, router)

	attempts := 0
	router.realtimeTranscriptAppendHook = func(threadID string, itemID string) error {
		attempts++
		if attempts == 1 {
			return errors.New("uncertain append")
		}
		return nil
	}
	if err := router.sealRealtimeTranscriptChunk(threadID, "user: ship it", "transcript_delta", "voice-1", []string{"frame-1"}); err != nil {
		t.Fatalf("seal = %v", err)
	}
	if attempts != 2 {
		t.Fatalf("append attempts = %d, want one retry", attempts)
	}
	if recorded := typedUserItems(t, store, threadID, "ship it"); len(recorded) != 1 {
		t.Fatalf("retried seal recorded %d messages", len(recorded))
	}
	if router.realtimeHistoryRecoveryRequired(threadID) {
		t.Fatal("a confirmed retry demanded history recovery")
	}
}

func TestRealtimeTranscriptSealFailureRequiresRecoveryLikeRust(t *testing.T) {
	router, store, sink, _ := newRealtimeTailRouter(t)
	threadID := startRealtimeTailThread(t, router)

	attempts := 0
	router.realtimeTranscriptAppendHook = func(threadID string, itemID string) error {
		attempts++
		return errors.New("store unavailable")
	}
	router.handleRealtimeEvent(threadID, realtime.Event{
		Type: "output_transcript.done",
		CompletedTranscript: []realtime.TranscriptEntry{
			{Role: "user", Text: "spoken request", ID: "frame-spoken"},
		},
		RealtimeSessionID: "voice-1",
	})
	if attempts != 2 {
		t.Fatalf("append attempts = %d, want the single retry Rust performs", attempts)
	}
	if !router.realtimeHistoryRecoveryRequired(threadID) {
		t.Fatal("an unconfirmed seal did not require history recovery")
	}
	if !sinkWaitsForError(sink, "realtime history recovery required; reload the thread before continuing") {
		t.Fatal("host was not told that recovery is required")
	}
	if recorded := typedUserItems(t, store, threadID, "spoken request"); len(recorded) != 0 {
		t.Fatalf("unconfirmed chunk reached history: %#v", recorded)
	}

	// A handoff on the same thread is refused while recovery is pending, so a
	// typed turn keeps its input instead of absorbing unconfirmed speech.
	router.handleRealtimeEvent(threadID, realtime.Event{
		Type:              "handoff.requested",
		HandoffID:         "handoff-1",
		ItemID:            "handoff-1",
		RealtimeSessionID: "voice-1",
		InputTranscript:   "spoken request",
	})
	if recorded := typedUserItems(t, store, threadID, "spoken request"); len(recorded) != 0 {
		t.Fatalf("handoff entered a turn while recovery was pending: %#v", recorded)
	}
	if attempts != 2 {
		t.Fatalf("handoff attempted another seal: attempts = %d", attempts)
	}
}

func TestRealtimeStartRequestNamesTheSessionToRecoverLikeRust(t *testing.T) {
	sessionID := "voice-1"
	cases := []struct {
		name   string
		params realtime.StartParams
		want   string
	}{
		{name: "explicit-session", params: realtime.StartParams{RealtimeSessionID: &sessionID}, want: "voice-1"},
		{name: "existing-call", params: realtime.StartParams{Transport: &realtime.StartTransport{Type: "existingCall", CallID: "call-9"}}, want: "call-9"},
		{name: "fresh-call", params: realtime.StartParams{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := realtimeRequestedSessionID(tc.params); got != tc.want {
				t.Fatalf("requested session = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestRealtimeReconnectReplaysUnconsumedSpeechFirstLikeRust(t *testing.T) {
	router, runtime, threadID, _ := openPersistedRealtimeThread(t)
	insertPersistedRealtimeItem(t, runtime, threadID, 0, map[string]any{"id": "voice-1:started", "realtime_session_id": "voice-1", "observed_at_ms": 100, "type": "realtime_session_started"})
	insertPersistedRealtimeItem(t, runtime, threadID, 1, map[string]any{"id": "frame-used", "realtime_session_id": "voice-1", "observed_at_ms": 110, "type": "frame", "channels": map[string]any{"voice_transcript": map[string]any{"role": "user", "text": "already sent"}}})
	insertPersistedRealtimeItem(t, runtime, threadID, 2, map[string]any{"id": "frame-pending", "realtime_session_id": "voice-1", "observed_at_ms": 120, "type": "frame", "channels": map[string]any{"voice_transcript": map[string]any{"role": "assistant", "text": "pending words"}}})
	insertPersistedRealtimeItem(t, runtime, threadID, 3, map[string]any{
		"id": "voice-1:chunk", "realtime_session_id": "voice-1", "observed_at_ms": 130, "type": "session_input_saved",
		"reason": "chunk", "source_event_ids": []string{"frame-used"}, "input_item_id": "input-1", "input": "already sent",
	})

	// The start hook restores the reattaching session's persisted speech before
	// the transport can stream newly captured frames.
	router.recoverRealtimeUnconsumedFrames(threadID, "voice-1")
	if ids := router.requireRealtime().RealtimeRecoveredFrameIDs(threadID); len(ids) != 1 || ids[0] != "frame-pending" {
		t.Fatalf("recovered frames = %#v", ids)
	}

	// Newly captured speech rendered through the handoff keeps the durable row
	// first, so model input order survives the reconnect.
	entries := router.prepareRealtimeRecordedEntries(threadID, []realtime.TranscriptEntry{{Role: "user", Text: "new words", ID: "frame-new"}})
	if len(entries) != 2 || entries[0].ID != "frame-pending" || entries[1].ID != "frame-new" {
		t.Fatalf("combined entries = %#v", entries)
	}
	router.consumeRealtimeRecoveredFrames(threadID)
	if ids := router.requireRealtime().RealtimeRecoveredFrameIDs(threadID); len(ids) != 0 {
		t.Fatalf("restored speech survived consumption: %#v", ids)
	}
}
