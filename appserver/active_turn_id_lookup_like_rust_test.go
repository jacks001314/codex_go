package appserver

import (
	"testing"

	"codex_go/turn"
)

// TestActiveTurnIDLookupMatchesSnapshotLikeRust covers Rust #46305: callers that
// only need the active turn id (interrupt validation, compaction abort, Guardian
// denial accounting) read it without materializing a turn snapshot, while the
// snapshot path keeps returning an independent copy.
func TestActiveTurnIDLookupMatchesSnapshotLikeRust(t *testing.T) {
	var nilManager *ThreadManager
	if got := nilManager.ActiveTurnID("thread"); got != "" {
		t.Fatalf("nil manager ActiveTurnID = %q, want empty", got)
	}
	manager := NewThreadManager(nil)
	if got := manager.ActiveTurnID("thread"); got != "" {
		t.Fatalf("idle manager ActiveTurnID = %q, want empty", got)
	}
	params := &turn.TurnStartParams{
		ThreadID: "thread",
		Input:    []turn.TurnUserInput{{Type: "text", Text: "hello"}},
	}
	if err := manager.RegisterTurn("thread", "turn-1", nil, 0, params); err != nil {
		t.Fatalf("RegisterTurn() error = %v", err)
	}
	// The id lookup shares the stored turn's identity and tolerates surrounding
	// whitespace like ActiveTurn.
	if got := manager.ActiveTurnID(" thread "); got != "turn-1" {
		t.Fatalf("ActiveTurnID = %q, want turn-1", got)
	}
	snapshot := manager.ActiveTurn("thread")
	if snapshot == nil || snapshot.TurnID != manager.ActiveTurnID("thread") {
		t.Fatalf("snapshot %#v disagrees with the id accessor", snapshot)
	}
	snapshot.Params.Input[0].Text = "mutated"
	if got := manager.ActiveTurn("thread").Params.Input[0].Text; got != "hello" {
		t.Fatalf("snapshot mutation leaked into the stored turn: %q", got)
	}
	// Guardian denial accounting uses the same non-cloning accessor.
	router := &RuntimeRouter{threads: manager}
	if got := router.guardianActiveTurnID("thread"); got != "turn-1" {
		t.Fatalf("guardianActiveTurnID = %q, want turn-1", got)
	}
	if _, ok := manager.ConsumeTurn("thread", "turn-1", false); !ok {
		t.Fatal("ConsumeTurn() did not consume the active turn")
	}
	if got := manager.ActiveTurnID("thread"); got != "" {
		t.Fatalf("ActiveTurnID after consumption = %q, want empty", got)
	}
	if got := router.guardianActiveTurnID("thread"); got != "" {
		t.Fatalf("guardianActiveTurnID after consumption = %q, want empty", got)
	}
}
