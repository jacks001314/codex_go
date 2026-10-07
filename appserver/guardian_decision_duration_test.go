package appserver

import (
	"context"
	"testing"

	"codex_go/model"
	"codex_go/state"
	"codex_go/telemetry"
)

const guardianDecisionDurationAssessment = `{"riskLevel":"low","userAuthorization":"high","outcome":"allow","rationale":"ok"}`

// Rust #51330 (f5fa209bb0, ext/guardian-reviewer/src/routing.rs
// `ReviewRequest::decide`): `codex.guardian.decision.duration_ms` times the
// complete approval decision -- fast decisions, preparation and cancellation
// included -- rather than only the model review segment that
// `codex.guardian.review.duration_ms` (Rust's emit_guardian_review_metrics)
// measures. Rust has no dedicated unit test for the timer; its placement at the
// top of `decide`, ahead of the cancellation and full-access arms, is what this
// mirrors.
func TestGuardianDecisionDurationCoversFastDecisionsLikeRust(t *testing.T) {
	// The full-access fast decision answers before any model review runs, so it
	// records the decision duration and no model-review duration.
	fastMetrics := state.NewTaskMetrics()
	fastHook := 0
	fastReviewer := &modelGuardianReviewer{
		agent: guardianAgentFunc(func(context.Context, *model.AgentRequest) (*model.AgentResponse, error) {
			t.Fatalf("the full-access decision must not run the model review")
			return nil, nil
		}),
		store:        state.NewReviewStore(),
		metrics:      fastMetrics,
		fullAccess:   func(string, string) bool { return true },
		fastDecision: func(context.Context, string, string, string) { fastHook++ },
	}
	decision, reason, err := fastReviewer.Review(context.Background(), "thread-1", "turn-1", "call-1", state.Action{Type: "command", Command: "ls", CWD: t.TempDir()})
	if err != nil || decision != state.DecisionApproved || reason != "full access" {
		t.Fatalf("fast decision = (%q, %q, %v), want the full-access approval", decision, reason, err)
	}
	if fastHook != 1 {
		t.Fatalf("fast-decision hook calls = %d, want 1", fastHook)
	}
	fastDurations := guardianMetricRecords(fastMetrics, telemetry.GuardianDecisionDurationMetric)
	if len(fastDurations) != 1 {
		t.Fatalf("decision durations for the fast decision = %d, want 1", len(fastDurations))
	}
	if fastDurations[0].Kind != "duration" || fastDurations[0].DurationMS < 0 || len(fastDurations[0].Tags) != 0 {
		t.Fatalf("decision duration record = %#v, want an untagged duration", fastDurations[0])
	}
	if got := len(guardianMetricRecords(fastMetrics, telemetry.GuardianReviewDurationMetric)); got != 0 {
		t.Fatalf("model-review durations for the fast decision = %d, want 0", got)
	}

	// A review that reaches the model records both durations: the total decision
	// (which also covers preparation) and the model-review segment.
	reviewMetrics := state.NewTaskMetrics()
	reviewReviewer := &modelGuardianReviewer{
		agent: guardianAgentFunc(func(context.Context, *model.AgentRequest) (*model.AgentResponse, error) {
			return &model.AgentResponse{Message: guardianDecisionDurationAssessment}, nil
		}),
		store:   state.NewReviewStore(),
		metrics: reviewMetrics,
	}
	if _, _, err := reviewReviewer.Review(context.Background(), "thread-2", "turn-2", "call-2", state.Action{Type: "command", Command: "ls", CWD: t.TempDir()}); err != nil {
		t.Fatalf("model review error = %v", err)
	}
	if got := len(guardianMetricRecords(reviewMetrics, telemetry.GuardianDecisionDurationMetric)); got != 1 {
		t.Fatalf("decision durations for the model review = %d, want 1", got)
	}
	if got := len(guardianMetricRecords(reviewMetrics, telemetry.GuardianReviewDurationMetric)); got != 1 {
		t.Fatalf("model-review durations for the model review = %d, want 1", got)
	}

	// A cancelled review still records the decision duration: the upstream timer
	// covers the cancellation arm as well.
	cancelMetrics := state.NewTaskMetrics()
	cancelReviewer := &modelGuardianReviewer{
		agent: guardianAgentFunc(func(context.Context, *model.AgentRequest) (*model.AgentResponse, error) {
			return nil, context.Canceled
		}),
		store:   state.NewReviewStore(),
		metrics: cancelMetrics,
	}
	cancelled, _, _ := cancelReviewer.Review(context.Background(), "thread-3", "turn-3", "call-3", state.Action{Type: "command", Command: "ls", CWD: t.TempDir()})
	if cancelled != state.DecisionAborted {
		t.Fatalf("cancelled decision = %q, want aborted", cancelled)
	}
	if got := len(guardianMetricRecords(cancelMetrics, telemetry.GuardianDecisionDurationMetric)); got != 1 {
		t.Fatalf("decision durations for the cancelled review = %d, want 1", got)
	}
}
