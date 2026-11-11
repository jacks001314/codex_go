package rollout

import (
	"testing"
	"time"
)

// TestLateAbortForUnknownTurnPreservesExplicitTurnLikeRust mirrors Rust #60700:
// an abort for a discarded turn must not interrupt the explicitly identified
// active turn, which still completes normally.
func TestLateAbortForUnknownTurnPreservesExplicitTurnLikeRust(t *testing.T) {
	lines := []Line{
		mustEventLine(t, map[string]any{"type": "turn_started", "turn_id": "turn-1"}),
		mustEventLine(t, map[string]any{"type": "turn_aborted", "turn_id": "discarded-turn", "reason": "replaced"}),
		mustEventLine(t, map[string]any{"type": "turn_complete", "turn_id": "turn-1"}),
	}
	_, turns := sessionItemsFromRolloutLines(lines, time.Unix(0, 0).UTC())
	if len(turns) != 1 {
		t.Fatalf("rollout turns = %#v, want only turn-1", turns)
	}
	if turns[0].ID != "turn-1" || turns[0].Status != "completed" {
		t.Fatalf("turn-1 = %#v, want completed (late abort preserved its ID and status)", turns[0])
	}
}

// TestAbortWithoutIDFinishesGeneratedTurnLikeRust pins the other half of Rust
// #60700: an abort with no turn ID still interrupts a turn opened without one,
// and that turn is finalized at the abort boundary.
func TestAbortWithoutIDFinishesGeneratedTurnLikeRust(t *testing.T) {
	lines := []Line{
		mustEventLine(t, map[string]any{"type": "turn_started"}),
		mustEventLine(t, map[string]any{"type": "turn_aborted", "reason": "interrupted"}),
	}
	_, turns := sessionItemsFromRolloutLines(lines, time.Unix(0, 0).UTC())
	if len(turns) != 1 || turns[0].Status != "interrupted" {
		t.Fatalf("rollout turns = %#v, want one interrupted generated turn", turns)
	}
}
