package appserver

import (
	"context"
	"testing"

	"codex_go/config"
	"codex_go/model"
	"codex_go/state"
	"codex_go/turn"
)

// TestModelGuardianReviewerRequiresFullReviewForProtectedModel mirrors Rust
// #41309: a model in auto_review.required_on_models cannot reuse a fast
// approval. Go has no cached-score auto-approval path, so the regression guard
// covers the remaining user-mode node_repl fast path.
func TestModelGuardianReviewerRequiresFullReviewForProtectedModel(t *testing.T) {
	reviews := 0
	fastDecisions := 0
	reviewer := newModelGuardianReviewer(guardianAgentFunc(func(_ context.Context, _ *model.AgentRequest) (*model.AgentResponse, error) {
		reviews++
		return &model.AgentResponse{
			Message: `{"riskLevel":"low","userAuthorization":"high","outcome":"allow","rationale":"reviewed"}`,
		}, nil
	})).(*modelGuardianReviewer)
	reviewer.approvalsReviewer = func(_, _ string) string {
		return string(config.ApprovalsReviewerUser)
	}
	reviewer.requiredReview = func(_, _ string) bool { return true }
	reviewer.fastDecision = func(context.Context, string, string, string) {
		fastDecisions++
	}

	decision, reason, err := reviewer.Review(
		context.Background(),
		"thread-protected",
		"turn-protected",
		"call-protected",
		state.Action{Type: "mcp_tool_call", Server: "node_repl", ToolName: "js"},
	)
	if err != nil {
		t.Fatalf("Review() error = %v", err)
	}
	if decision != state.DecisionApproved || reason != "reviewed" {
		t.Fatalf("Review() = %q, %q, want approved by full review", decision, reason)
	}
	if reviews != 1 {
		t.Fatalf("full reviews = %d, want 1", reviews)
	}
	if fastDecisions != 0 {
		t.Fatalf("fast decisions = %d, want 0", fastDecisions)
	}
}

func TestProtectedModelForcesAutoReviewerForTurn(t *testing.T) {
	cfg := &config.Config{
		Values: map[string]any{"approvals_reviewer": "user"},
		Requirements: &config.ConfigRequirements{
			AutoReview: &config.AutoReviewRequirements{RequiredOnModels: []string{"protected-model"}},
		},
	}
	autoReview := string(config.ApprovalsReviewerAutoReview)
	user := string(config.ApprovalsReviewerUser)
	for _, reviewer := range []*string{&autoReview, &user, nil} {
		params := &turn.TurnStartParams{Model: "protected-model", ApprovalsReviewer: reviewer}
		if got := turnApprovalsReviewerForTurn(cfg, params); got != string(config.ApprovalsReviewerAutoReview) {
			t.Fatalf("turnApprovalsReviewerForTurn(%v) = %q, want auto_review", reviewer, got)
		}
	}
	cfg.Requirements.AutoReview.RequiredOnModels = []string{"other-model"}
	if got := turnApprovalsReviewerForTurn(cfg, &turn.TurnStartParams{Model: "protected-model"}); got != "user" {
		t.Fatalf("turnApprovalsReviewerForTurn(unprotected) = %q, want user", got)
	}
}
