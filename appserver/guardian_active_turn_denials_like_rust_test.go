package appserver

import (
	"testing"

	"codex_go/state"
	"codex_go/telemetry"
)

// Mirrors Rust #44866: Guardian denial accounting and interruption follow the
// session's active turn, so a review dispatched by a cell from an earlier turn
// still counts against - and can interrupt - the turn that is running now.
func TestGuardianDenialsAccountToActiveTurnLikeRust(t *testing.T) {
	metrics := state.NewTaskMetrics()
	interruptedTurn := ""
	reviewer := &modelGuardianReviewer{
		breaker: state.NewCircuitBreaker(),
		metrics: metrics,
		interrupt: func(threadID, turnID, reason string) {
			if threadID != "thread-1" {
				t.Fatalf("interrupt thread = %q", threadID)
			}
			interruptedTurn = turnID
		},
		activeTurnID: func(threadID string) string {
			if threadID != "thread-1" {
				t.Fatalf("accounting thread = %q", threadID)
			}
			return "turn-active"
		},
	}
	// The review still reports the originating turn, but accounting must use
	// the active one.
	for i := 0; i < state.MaxConsecutiveDenialsPerTurn; i++ {
		reviewer.recordDecision("thread-1", "turn-originating", state.DecisionDenied, "denied")
	}
	if interruptedTurn != "turn-active" {
		t.Fatalf("interrupted turn = %q, want turn-active", interruptedTurn)
	}
	if got := len(guardianMetricRecords(metrics, telemetry.GuardianDenialLimitReachedMetric)); got != 1 {
		t.Fatalf("denial-limit counters = %d, want 1", got)
	}

	// An approval that belongs to the stale review resets the active turn's
	// window, so the limit needs a full new window before interrupting again.
	resetReviewer := &modelGuardianReviewer{
		breaker:      state.NewCircuitBreaker(),
		activeTurnID: func(string) string { return "turn-active" },
	}
	for i := 0; i < state.MaxConsecutiveDenialsPerTurn-1; i++ {
		resetReviewer.recordDecision("thread-1", "turn-originating", state.DecisionDenied, "")
	}
	resetReviewer.recordDecision("thread-1", "turn-originating", state.DecisionApproved, "")
	interrupted := 0
	resetReviewer.interrupt = func(string, string, string) { interrupted++ }
	for i := 0; i < state.MaxConsecutiveDenialsPerTurn-1; i++ {
		resetReviewer.recordDecision("thread-1", "turn-originating", state.DecisionDenied, "")
	}
	if interrupted != 0 {
		t.Fatalf("interrupts after an approval reset = %d, want 0", interrupted)
	}
}

// With no active turn there is nothing to account for: Rust returns without
// recording a decision, so a finished turn is never charged retroactively.
func TestGuardianDecisionsSkipWithoutActiveTurnLikeRust(t *testing.T) {
	metrics := state.NewTaskMetrics()
	interrupted := 0
	reviewer := &modelGuardianReviewer{
		breaker:      state.NewCircuitBreaker(),
		metrics:      metrics,
		activeTurnID: func(string) string { return "" },
		interrupt:    func(string, string, string) { interrupted++ },
	}
	for i := 0; i < state.MaxConsecutiveDenialsPerTurn; i++ {
		reviewer.recordDecision("thread-1", "turn-finished", state.DecisionDenied, "")
	}
	if interrupted != 0 {
		t.Fatalf("interrupts = %d, want 0", interrupted)
	}
	if got := len(guardianMetricRecords(metrics, telemetry.GuardianDenialLimitReachedMetric)); got != 0 {
		t.Fatalf("denial-limit counters = %d, want 0", got)
	}
}
