package appserver

// Mirrors Rust #62585: a refresh that unblocks a model wakes the automatic work
// that waited behind the block, while a refresh that is still blocked wakes
// nothing.

import (
	"testing"
	"time"

	"codex_go/config"
	"codex_go/turn"
)

func TestGuardianPolicyRefreshWakesBlockedWorkLikeRust(t *testing.T) {
	manager := NewThreadManager(nil)
	if err := manager.RegisterTurn("thread-4", "turn-4", func() {}, time.Now().UnixMilli(), &turn.TurnStartParams{ThreadID: "thread-4"}); err != nil {
		t.Fatalf("RegisterTurn() error = %v", err)
	}
	manager.UpdateTurn("thread-4", "turn-4", func(active *activeRuntimeTurn) {
		active.RunConfig = &appTurnRunConfig{
			Model:             "gpt-5.4",
			ApprovalsReviewer: string(config.ApprovalsReviewerUser),
			Config:            &config.Config{Values: map[string]any{}},
		}
	})
	router := NewRuntimeRouter(RuntimeServices{Config: config.NewConfigService(t.TempDir())})
	router.threads = manager
	router.active = manager.active
	router.turnsMu = &manager.turnsMu
	var wakes []string
	router.guardianPolicyWake = func(threadID string) { wakes = append(wakes, threadID) }

	policy := "blocked policy"
	blocked := &config.GuardianPolicySnapshot{
		Revision: 12,
		Requirements: &config.ConfigRequirements{
			GuardianPolicyConfig:      &policy,
			AutoReview:                &config.AutoReviewRequirements{RequiredOnModels: []string{"gpt-5.4"}},
			AllowedApprovalsReviewers: []config.ApprovalsReviewer{config.ApprovalsReviewerUser},
		},
	}
	if rejection := router.applyGuardianPolicyToThread("thread-4", blocked); rejection == nil {
		t.Fatal("blocking refresh reported no rejection")
	}
	if len(wakes) != 0 {
		t.Fatalf("a still-blocked thread woke work: %v", wakes)
	}

	permit := guardianPolicyTestSnapshot(13, "permitted policy", []config.ApprovalsReviewer{config.ApprovalsReviewerAutoReview})
	if rejection := router.applyGuardianPolicyToThread("thread-4", permit); rejection != nil {
		t.Fatalf("permitting refresh rejected the turn: %v", rejection)
	}
	if len(wakes) != 1 || wakes[0] != "thread-4" {
		t.Fatalf("wakes = %v, want the unblocked thread woken once", wakes)
	}

	// A later permitting refresh is not a transition and wakes nothing again.
	if rejection := router.applyGuardianPolicyToThread("thread-4", guardianPolicyTestSnapshot(14, "permitted policy", []config.ApprovalsReviewer{config.ApprovalsReviewerAutoReview})); rejection != nil {
		t.Fatalf("second permitting refresh rejected the turn: %v", rejection)
	}
	if len(wakes) != 1 {
		t.Fatalf("wakes = %v, want no second wake", wakes)
	}
	if active := manager.ActiveTurn("thread-4"); active == nil || active.GuardianPolicyBlocked {
		t.Fatal("the permitted turn stayed blocked")
	}
}

// A live turn resumes on its next step check, so the pending-work wake leaves
// it alone (Rust #62585 notifies the queue; only an idle thread starts a turn).
func TestGuardianPolicyWakeSkipsLiveTurnLikeRust(t *testing.T) {
	manager := NewThreadManager(nil)
	if err := manager.RegisterTurn("thread-5", "turn-5", func() {}, time.Now().UnixMilli(), &turn.TurnStartParams{ThreadID: "thread-5"}); err != nil {
		t.Fatalf("RegisterTurn() error = %v", err)
	}
	router := NewRuntimeRouter(RuntimeServices{})
	router.threads = manager
	router.active = manager.active
	router.wakeGuardianPolicyBlockedWork("thread-5")
	router.agentWakeRetryMu.Lock()
	retries := len(router.agentWakeRetry)
	router.agentWakeRetryMu.Unlock()
	if retries != 0 {
		t.Fatalf("a live turn queued an admission retry: %v", router.agentWakeRetry)
	}
}
