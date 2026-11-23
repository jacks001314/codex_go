package turn

import (
	"testing"

	"codex_go/model"
)

func textResultSource(text string) model.ToolResultBodySource {
	return model.ToolResultBodySource{
		Content: []any{map[string]any{"type": "text", "text": text}},
	}
}

// Rust #62434 re-checks that the capture owner is current under the retained
// bodies lock before keeping an MCP result. A capture that lost the race with a
// replacement store must not return a local ID that no later request attaches.
func TestResultBodyCaptureRechecksCurrentStoreUnderLockLikeRust(t *testing.T) {
	recorder := NewExecutedToolCallRecorder()
	recorder.BeginResultBodyTurn("turn-1", "chatgpt|acct", true)
	store := recorder.currentResultBodyTurn()
	if store == nil {
		t.Fatal("BeginResultBodyTurn() recorded no store")
	}
	current := func() bool { return recorder.currentResultBodyTurn() == store }

	id, ok := store.recordNestedIfCurrent(textResultSource("first"), "", current)
	if !ok || id == "" {
		t.Fatalf("recordNestedIfCurrent() = %q, %v, want a local id", id, ok)
	}

	// A replacement store for the same turn and owner is what a concurrent
	// turn transition looks like to an in-flight capture.
	recorder.BeginResultBodyTurn("turn-1", "chatgpt|acct", true)
	if id, ok := store.recordNestedIfCurrent(textResultSource("late"), "", current); ok || id != "" {
		t.Fatalf("late capture retained %q after the store was replaced", id)
	}
	store.mu.Lock()
	retained := len(store.byID)
	store.mu.Unlock()
	if retained != 1 {
		t.Fatalf("replaced store retained %d bodies, want only the first", retained)
	}

	// The same recorder path still records against the live store.
	live := recorder.currentResultBodyTurn()
	if id, ok := live.recordNestedIfCurrent(textResultSource("second"), "", func() bool { return recorder.currentResultBodyTurn() == live }); !ok || id == "" {
		t.Fatalf("live capture = %q, %v", id, ok)
	}
}

// The recorder binds a capture to the store it looked up, so a capture that
// arrives after the turn's store moved on attaches no observation.
func TestCaptureResultBodyAfterStoreReplacementLikeRust(t *testing.T) {
	recorder := NewExecutedToolCallRecorder()
	recorder.BeginResultBodyTurn("turn-1", "chatgpt|acct", true)
	old := recorder.currentResultBodyTurn()
	recorder.BeginResultBodyTurn("turn-1", "chatgpt|acct", true)
	if recorder.currentResultBodyTurn() == old {
		t.Fatal("BeginResultBodyTurn() did not replace the store")
	}
	if id, ok := recorder.CaptureResultBody("turn-1", "chatgpt|acct", textResultSource("late")); !ok || id == "" {
		t.Fatalf("capture against the live store = %q, %v", id, ok)
	}
	if id, ok := old.Record(textResultSource("orphan")); !ok || id == "" {
		t.Fatalf("direct store record = %q, %v", id, ok)
	}
	if _, ok := recorder.CaptureResultBody("turn-1", "chatgpt|other", textResultSource("wrong owner")); ok {
		t.Fatal("capture accepted a foreign credential owner")
	}
}
