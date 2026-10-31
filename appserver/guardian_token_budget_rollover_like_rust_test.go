package appserver

import (
	"context"
	"testing"

	"codex_go/config"
	"codex_go/features"
	"codex_go/model"
	"codex_go/state"
)

func TestGuardianTokenBudgetRolloverStartsFreshReviewChainLikeRust(t *testing.T) {
	newReviewer := func(rollover bool) (*modelGuardianReviewer, *guardianSessionRunner) {
		t.Helper()
		runner := &guardianSessionRunner{agent: guardianAgentFunc(func(context.Context, *model.AgentRequest) (*model.AgentResponse, error) {
			return &model.AgentResponse{Message: `{"riskLevel":"low","userAuthorization":"high","outcome":"allow","rationale":"ok"}`}, nil
		})}
		runner.previous = "resp-prior-review"
		reviewer := &modelGuardianReviewer{
			agent: runner, store: state.NewReviewStore(), breaker: state.NewCircuitBreaker(),
			scoreProgress: map[string]*guardianScoreProgress{}, maxToolCallLag: defaultGuardianMaxToolCallLag,
		}
		// The gate added for Rust #43912 only consults the rollover callback when
		// the reviewer's own config enables token-budget mode.
		reviewer.reviewPlan = func(string, string) guardianReviewPlan {
			return guardianReviewPlan{Prepared: &guardianReviewerPreparedOptions{Config: &config.Config{
				Values: map[string]any{"features": map[string]any{"token_budget": true}},
			}}}
		}
		reviewer.rolloverForTokenBudget = func(string, string) bool { return rollover }
		return reviewer, runner
	}

	reviewer, runner := newReviewer(true)
	var rolledPrevious string
	runner.agent = guardianAgentFunc(func(_ context.Context, request *model.AgentRequest) (*model.AgentResponse, error) {
		rolledPrevious = request.PreviousResponseID
		return &model.AgentResponse{Message: `{"riskLevel":"low","userAuthorization":"high","outcome":"allow","rationale":"ok"}`}, nil
	})
	if _, _, err := reviewer.Review(context.Background(), "thread-budget", "turn-budget", "call-budget", state.Action{Type: "command", Command: "ls", CWD: "/repo"}); err != nil {
		t.Fatalf("Review() error = %v", err)
	}
	if rolledPrevious != "" {
		t.Fatalf("rollover previous response = %q, want a fresh chain", rolledPrevious)
	}

	reviewer, runner = newReviewer(false)
	var seenPrevious string
	runner.agent = guardianAgentFunc(func(_ context.Context, request *model.AgentRequest) (*model.AgentResponse, error) {
		seenPrevious = request.PreviousResponseID
		return &model.AgentResponse{Message: `{"riskLevel":"low","userAuthorization":"high","outcome":"allow","rationale":"ok"}`}, nil
	})
	if _, _, err := reviewer.Review(context.Background(), "thread-budget", "turn-budget", "call-budget", state.Action{Type: "command", Command: "ls", CWD: "/repo"}); err != nil {
		t.Fatalf("Review() error = %v", err)
	}
	if seenPrevious != "resp-prior-review" {
		t.Fatalf("previous response = %q, want resp-prior-review", seenPrevious)
	}
}

// TestGuardianReviewSkipsParentTokenBudgetRolloverLikeRust mirrors Rust #43912:
// the reviewer keeps summary-based compaction even when the parent turn and the
// model defaults enable token-budget mode.
func TestGuardianReviewSkipsParentTokenBudgetRolloverLikeRust(t *testing.T) {
	parent := &config.Config{Values: map[string]any{
		"features": map[string]any{
			"token_budget":       true,
			"context_management": map[string]any{"experimental_mode": true},
		},
	}}
	prepared, err := guardianPrepareReviewerOptions(guardianReviewerPrepareInput{
		ParentConfig:     parent,
		ParentModel:      "gpt-parent",
		ReviewerModel:    "codex-auto-review",
		BaseInstructions: "policy",
	})
	if err != nil {
		t.Fatalf("guardianPrepareReviewerOptions() error = %v", err)
	}
	for _, key := range []string{"token_budget", "context_management"} {
		if features.Enabled(prepared.Config.FeatureSettings(), key) {
			t.Fatalf("reviewer feature %q enabled from parent", key)
		}
	}
	if guardianReviewerTokenBudgetEnabled(prepared) {
		t.Fatal("reviewer token budget enabled despite the isolated reviewer config")
	}
	// The explicit disabled value also keeps model defaults from reactivating it.
	budget, err := prepared.Config.TokenBudgetConfigWithDefaults(&config.TokenBudgetDefaults{
		Enabled:                         true,
		ReminderThresholdTokens:         1000,
		AutoCompactFallbackBufferTokens: 100,
	})
	if err != nil {
		t.Fatalf("TokenBudgetConfigWithDefaults() error = %v", err)
	}
	if budget.Enabled {
		t.Fatal("model defaults reactivated the reviewer token budget")
	}

	runner := &guardianSessionRunner{agent: guardianAgentFunc(func(_ context.Context, request *model.AgentRequest) (*model.AgentResponse, error) {
		if request.PreviousResponseID != "resp-prior-review" {
			t.Fatalf("previous response = %q, want the reuse chain", request.PreviousResponseID)
		}
		return &model.AgentResponse{Message: `{"riskLevel":"low","userAuthorization":"high","outcome":"allow","rationale":"ok"}`}, nil
	})}
	runner.previous = "resp-prior-review"
	reviewer := &modelGuardianReviewer{
		agent: runner, store: state.NewReviewStore(), breaker: state.NewCircuitBreaker(),
		scoreProgress: map[string]*guardianScoreProgress{}, maxToolCallLag: defaultGuardianMaxToolCallLag,
	}
	reviewer.reviewPlan = func(string, string) guardianReviewPlan {
		return guardianReviewPlan{Prepared: prepared}
	}
	consulted := false
	reviewer.rolloverForTokenBudget = func(string, string) bool {
		consulted = true
		return true
	}
	if _, _, err := reviewer.Review(context.Background(), "thread-budget", "turn-budget", "call-budget", state.Action{Type: "command", Command: "ls", CWD: "/repo"}); err != nil {
		t.Fatalf("Review() error = %v", err)
	}
	if consulted {
		t.Fatal("parent token budget was consulted for a reviewer without token-budget mode")
	}
}
