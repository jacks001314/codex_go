package appserver

// Mirrors Rust #62585: automatic work stays queued while the published Guardian
// policy rejects the settings the wake would start with, and a later permitting
// refresh wakes the parked thread exactly once.

import (
	"testing"

	"codex_go/config"
)

func guardianPolicyParkedThreads(r *RuntimeRouter) []string {
	r.guardianPolicyParkedMu.Lock()
	defer r.guardianPolicyParkedMu.Unlock()
	out := make([]string, 0, len(r.guardianPolicyParked))
	for threadID := range r.guardianPolicyParked {
		out = append(out, threadID)
	}
	return out
}

func TestGuardianPolicyParksAutomaticWakeUntilPermittingRefreshLikeRust(t *testing.T) {
	router, _ := newRecordedAgentMailWakeRouter(t, true)
	blocking := &config.ConfigRequirements{
		AutoReview:                &config.AutoReviewRequirements{RequiredOnModels: []string{"gpt-mail-test"}},
		AllowedApprovalsReviewers: []config.ApprovalsReviewer{config.ApprovalsReviewerUser},
	}
	router.services.Config.PublishGuardianPolicyRequirements(blocking)

	if router.wakeRootForRecordedAgentMail("root") {
		t.Fatal("a blocked automatic wake started a turn")
	}
	if active := router.threads.ActiveTurn("root"); active != nil {
		router.cancelActiveRuntimeTurn("root", active.TurnID)
		t.Fatalf("blocked wake started a turn: %#v", active)
	}
	if parked := guardianPolicyParkedThreads(router); len(parked) != 1 || parked[0] != "root" {
		t.Fatalf("parked threads = %v, want the refused root", parked)
	}

	// A refresh that still blocks keeps the work queued.
	router.services.Config.PublishGuardianPolicyRequirements(blocking)
	router.retryGuardianPolicyParkedWakes()
	if active := router.threads.ActiveTurn("root"); active != nil {
		router.cancelActiveRuntimeTurn("root", active.TurnID)
		t.Fatalf("a still-blocking refresh woke the root: %#v", active)
	}
	if parked := guardianPolicyParkedThreads(router); len(parked) != 1 {
		t.Fatalf("parked threads after a blocking refresh = %v, want the root still queued", parked)
	}

	// Lifting the restriction wakes the parked work.
	router.services.Config.PublishGuardianPolicyRequirements(&config.ConfigRequirements{})
	router.retryGuardianPolicyParkedWakes()
	active := router.threads.ActiveTurn("root")
	if active == nil || active.Params == nil {
		t.Fatal("a permitting refresh did not wake the parked root")
	}
	if !active.Params.AutomaticWake {
		t.Fatalf("wake turn params = %#v, want AutomaticWake", active.Params)
	}
	if parked := guardianPolicyParkedThreads(router); len(parked) != 0 {
		t.Fatalf("parked threads after the wake = %v, want none", parked)
	}

	// A later retry is not a transition and does not start a second turn.
	router.retryGuardianPolicyParkedWakes()
	if got := router.threads.ActiveTurn("root"); got == nil || got.TurnID != active.TurnID {
		t.Fatalf("second retry changed the active turn: %#v", got)
	}
	router.cancelActiveRuntimeTurn("root", active.TurnID)
}
