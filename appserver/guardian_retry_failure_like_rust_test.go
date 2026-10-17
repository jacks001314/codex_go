package appserver

import (
	"context"
	"strings"
	"testing"
	"time"

	"codex_go/codexapi"
	"codex_go/model"
	"codex_go/state"
)

// TestGuardianReviewFailureLeavesAssessmentUnsetLikeRust pins Rust #44482's
// reporting half: a non-transient failure is not retried, the action stays
// denied, and risk/authorization stay unset with a review-failure explanation.
func TestGuardianReviewFailureLeavesAssessmentUnsetLikeRust(t *testing.T) {
	attempts := 0
	reviewer := &modelGuardianReviewer{
		agent: guardianAgentFunc(func(_ context.Context, _ *model.AgentRequest) (*model.AgentResponse, error) {
			attempts++
			return nil, &codexapi.APIError{Kind: codexapi.ErrorInvalidRequest, Status: 400, Message: "non-transient failure"}
		}),
		store: state.NewReviewStore(),
	}
	decision, message, err := reviewer.Review(context.Background(), "thread-failure", "turn-failure", "call-failure", state.Action{
		Type: "command", Command: "ls", CWD: "/repo",
	})
	if err != nil {
		t.Fatalf("Review() error = %v", err)
	}
	if decision != state.DecisionDenied {
		t.Fatalf("decision = %q, want denied", decision)
	}
	if attempts != 1 {
		t.Fatalf("attempts = %d, want a non-transient HTTP failure to fail closed", attempts)
	}
	if !strings.Contains(message, "This is a review failure, not a determination that the action is unsafe.") {
		t.Fatalf("message = %q", message)
	}
	event, ok := reviewer.store.Get("guardian-review-1")
	if !ok || event == nil {
		t.Fatalf("review event = %#v, %v", event, ok)
	}
	if event.Status != state.StatusDenied || event.RiskLevel != nil || event.UserAuthorization != nil {
		t.Fatalf("failed review assessment fields = %#v", event)
	}
}

// TestGuardianReviewHonorsServerRetryDelayLikeRust pins the retry half: a
// retryable rate limit is retried and the server's requested delay is honored
// instead of the shorter service backoff.
func TestGuardianReviewHonorsServerRetryDelayLikeRust(t *testing.T) {
	attempts := 0
	reviewer := &modelGuardianReviewer{
		agent: guardianAgentFunc(func(_ context.Context, _ *model.AgentRequest) (*model.AgentResponse, error) {
			attempts++
			if attempts == 1 {
				return nil, (&codexapi.APIError{
					Kind:    codexapi.ErrorRateLimitExceeded,
					Status:  429,
					Message: "rate limited",
				}).WithRetryDelay(60 * time.Millisecond)
			}
			return &model.AgentResponse{Message: `{"riskLevel":"low","userAuthorization":"high","outcome":"allow","rationale":"ok"}`}, nil
		}),
		store: state.NewReviewStore(),
	}
	started := time.Now()
	decision, _, err := reviewer.Review(context.Background(), "thread-retry", "turn-retry", "call-retry", state.Action{
		Type: "command", Command: "ls", CWD: "/repo",
	})
	elapsed := time.Since(started)
	if err != nil {
		t.Fatalf("Review() error = %v", err)
	}
	if attempts != 2 || decision != state.DecisionApproved {
		t.Fatalf("attempts/decision = %d/%q", attempts, decision)
	}
	if elapsed < 50*time.Millisecond {
		t.Fatalf("elapsed = %v, want the 60ms server retry delay honored", elapsed)
	}
}
