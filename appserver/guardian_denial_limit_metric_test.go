package appserver

import (
	"testing"

	"codex_go/state"
	"codex_go/telemetry"
)

// Rust #51334 (79cae5f7fb, ext/guardian-reviewer/src/review.rs): when the
// denial window returns its once-per-turn warning, the reviewer increments
// `codex.guardian.denial_limit_reached` immediately before interrupting the
// turn. Rust has no dedicated test for the counter; the warning's
// once-per-turn behavior is what the review.rs denial branch relies on.
func TestGuardianDenialLimitReachedMetricLikeRust(t *testing.T) {
	metrics := state.NewTaskMetrics()
	interrupted := 0
	reviewer := &modelGuardianReviewer{
		breaker:   state.NewCircuitBreaker(),
		metrics:   metrics,
		interrupt: func(threadID, turnID string) { interrupted++ },
	}

	for i := 0; i < state.MaxConsecutiveDenialsPerTurn; i++ {
		reviewer.recordDecision("thread-1", "turn-1", state.DecisionDenied)
	}
	if interrupted != 1 {
		t.Fatalf("interrupts = %d, want 1 (the denial limit interrupts once)", interrupted)
	}
	if got := len(guardianMetricRecords(metrics, telemetry.GuardianDenialLimitReachedMetric)); got != 1 {
		t.Fatalf("denial-limit counters = %d, want 1", got)
	}

	// The denial window warns once per turn: further denials in the same turn do
	// not count again and do not interrupt again.
	reviewer.recordDecision("thread-1", "turn-1", state.DecisionDenied)
	if interrupted != 1 {
		t.Fatalf("interrupts after the warning = %d, want 1", interrupted)
	}
	if got := len(guardianMetricRecords(metrics, telemetry.GuardianDenialLimitReachedMetric)); got != 1 {
		t.Fatalf("denial-limit counters after the warning = %d, want 1", got)
	}

	// A fresh turn gets its own window and its own counter.
	for i := 0; i < state.MaxConsecutiveDenialsPerTurn; i++ {
		reviewer.recordDecision("thread-1", "turn-2", state.DecisionDenied)
	}
	if interrupted != 2 {
		t.Fatalf("interrupts for the second turn = %d, want 2", interrupted)
	}
	if got := len(guardianMetricRecords(metrics, telemetry.GuardianDenialLimitReachedMetric)); got != 2 {
		t.Fatalf("denial-limit counters for the second turn = %d, want 2", got)
	}

	// Approvals and repeated denials below the limit never count.
	belowLimit := state.NewTaskMetrics()
	belowReviewer := &modelGuardianReviewer{breaker: state.NewCircuitBreaker(), metrics: belowLimit}
	for i := 0; i < state.MaxConsecutiveDenialsPerTurn-1; i++ {
		belowReviewer.recordDecision("thread-2", "turn-3", state.DecisionDenied)
	}
	belowReviewer.recordDecision("thread-2", "turn-3", state.DecisionApproved)
	if got := len(guardianMetricRecords(belowLimit, telemetry.GuardianDenialLimitReachedMetric)); got != 0 {
		t.Fatalf("denial-limit counters below the limit = %d, want 0", got)
	}
}
