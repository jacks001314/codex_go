package appserver

import (
	"context"
	"testing"

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
