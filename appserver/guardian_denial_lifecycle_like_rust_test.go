package appserver

import (
	"strings"
	"testing"

	"codex_go/state"
)

// Rust parity: codex-rs/ext/guardian-v2/src/sync_reviewer/lifecycle_tests.rs
// (`terminal_turn_clears_extension_owned_denials`). Rust #45672 moved denial
// cleanup out of core task handling and into Guardian's turn start, stop and
// abort hooks, so a completed or interrupted turn never leaves a stale denial
// window behind for the next review to inherit.
func TestGuardianTurnHooksClearStaleDenialsLikeRust(t *testing.T) {
	breaker := state.NewCircuitBreaker()
	router := &RuntimeRouter{services: RuntimeServices{
		GuardianReviewer: &modelGuardianReviewer{breaker: breaker},
	}}
	seed := func(turnID string) {
		t.Helper()
		for attempt := 0; attempt < state.MaxConsecutiveDenialsPerTurn-1; attempt++ {
			if action := breaker.RecordDenial(turnID); action.InterruptTurn {
				t.Fatalf("seed denial %d interrupted early", attempt)
			}
		}
	}

	// Control: the same seed reaches the limit without a lifecycle hook.
	seed("turn-control")
	if action := breaker.RecordDenial("turn-control"); !action.InterruptTurn {
		t.Fatal("control turn did not reach the denial limit; the seed is ineffective")
	}

	for _, testCase := range []struct {
		name  string
		hooks func(turnID string)
	}{
		{name: "turn start", hooks: func(turnID string) { router.beginGuardianTurn("thread-1", turnID) }},
		{name: "turn complete", hooks: func(turnID string) { router.endGuardianTurn("thread-1", turnID, "completed") }},
		{name: "turn aborted", hooks: func(turnID string) { router.endGuardianTurn("thread-1", turnID, "aborted") }},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			turnID := "turn-" + strings.ReplaceAll(testCase.name, " ", "-")
			seed(turnID)
			testCase.hooks(turnID)
			if action := breaker.RecordDenial(turnID); action.InterruptTurn {
				t.Fatalf("%s kept stale denials", testCase.name)
			}
		})
	}

	// A stub reviewer has no accounting to clear; the hooks stay a no-op.
	stub := &RuntimeRouter{services: RuntimeServices{
		GuardianReviewer: &nativeToolReviewStubReviewer{decision: state.DecisionApproved},
	}}
	stub.beginGuardianTurn("thread-1", "turn-stub")
	stub.endGuardianTurn("thread-1", "turn-stub", "completed")
}
