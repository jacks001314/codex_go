package appserver

// Mirrors Rust #62299's reconnect restore: the newest persisted session-end
// receipt is the speech a reattached call puts before newly captured speech.

import (
	"encoding/json"
	"testing"

	"codex_go/state"
)

func realtimeReceiptItem(t *testing.T, item map[string]any) state.ThreadRealtimeEntry {
	t.Helper()
	encoded, err := json.Marshal(item)
	if err != nil {
		t.Fatal(err)
	}
	return state.ThreadRealtimeEntry{ItemJSON: encoded}
}

func TestRealtimeSessionEndReceiptFoundLikeRust(t *testing.T) {
	observed := int64(1_900)
	session := &state.ThreadRealtimeSession{SessionID: "voice-1", Items: []state.ThreadRealtimeEntry{
		realtimeReceiptItem(t, map[string]any{"type": "realtime_session_started"}),
		realtimeReceiptItem(t, map[string]any{"type": "session_input_saved", "reason": "session_end", "input": "pending words", "observed_at_ms": observed}),
		realtimeReceiptItem(t, map[string]any{"type": "realtime_session_closed", "outcome": "ended"}),
	}}
	text, gotObserved := realtimeSessionEndReceipt(session)
	if text != "pending words" || gotObserved == nil || *gotObserved != observed {
		t.Fatalf("receipt = %q/%v, want the persisted speech", text, gotObserved)
	}

	consumed := &state.ThreadRealtimeSession{SessionID: "voice-2", Items: []state.ThreadRealtimeEntry{
		realtimeReceiptItem(t, map[string]any{"type": "realtime_session_started"}),
		realtimeReceiptItem(t, map[string]any{"type": "session_input_saved", "reason": "chunk", "input": "already delivered"}),
	}}
	if text, _ := realtimeSessionEndReceipt(consumed); text != "" {
		t.Fatalf("chunk receipt restored as pending speech: %q", text)
	}
	if text, _ := realtimeSessionEndReceipt(nil); text != "" {
		t.Fatalf("nil session restored %q", text)
	}
}
