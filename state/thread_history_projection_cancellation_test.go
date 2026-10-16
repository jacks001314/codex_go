package state

import (
	"context"
	"errors"
	"testing"

	"codex_go/rollout"
)

// TestReadThreadHistoryProjectionStepsStopsWhenCallerCancelledLikeRust mirrors
// Rust #53947's materialization regression
// (thread-store/src/local/thread_history_materialization_tests.rs cancelling
// between records): the caller's context becomes the rollout reader's
// cancellation predicate, and a cancelled read is an error, not a short
// history.
func TestReadThreadHistoryProjectionStepsStopsWhenCallerCancelledLikeRust(t *testing.T) {
	_, path := openProjectionFixture(t, historyMetaLine(0)+historyTurnLine(1, "turn-1", "2025-01-01T00:00:00Z"))

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	steps, _, err := readThreadHistoryProjectionSteps(cancelled, path, 0, 0, nil)
	if !errors.Is(err, rollout.ErrRolloutReadCancelled) {
		t.Fatalf("cancelled read error = %v, want %v", err, rollout.ErrRolloutReadCancelled)
	}
	if steps != nil {
		t.Fatalf("cancelled read steps = %#v, want none", steps)
	}

	// Control: a live caller reads the same records.
	steps, _, err = readThreadHistoryProjectionSteps(context.Background(), path, 0, 0, nil)
	if err != nil || len(steps) != 2 {
		t.Fatalf("live read = steps %#v err %v, want the meta and turn records", steps, err)
	}
}

// TestMaterializeThreadHistoryWithCancelledCallerWritesNothingLikeRust pins the
// caller-visible half of Rust #53947: a cancelled caller cannot end up with a
// partially projected history or an advanced checkpoint, while the live caller
// materializes and checkpoints normally.
func TestMaterializeThreadHistoryWithCancelledCallerWritesNothingLikeRust(t *testing.T) {
	db, path := openProjectionFixture(t, historyMetaLine(0)+historyTurnLine(1, "turn-1", "2025-01-01T00:00:00Z"))

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := MaterializeThreadHistory(cancelled, db, "thread-1", path, 0, nil); err == nil {
		t.Fatal("MaterializeThreadHistory(cancelled) = nil, want the cancellation error")
	}
	var rows int
	if err := db.QueryRow(`SELECT COUNT(*) FROM thread_history_projection_state WHERE thread_id = ?`, "thread-1").Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 0 {
		t.Fatalf("projection checkpoints = %d, want none after cancellation", rows)
	}

	if err := MaterializeThreadHistory(context.Background(), db, "thread-1", path, 0, nil); err != nil {
		t.Fatalf("MaterializeThreadHistory(live) error = %v", err)
	}
	state := requireProjectionState(t, db)
	if state.NextRolloutOrdinal != 2 {
		t.Fatalf("checkpoint = %+v, want ordinal 2 after the live read", state)
	}
}
