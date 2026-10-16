package appserver

import (
	"context"
	"testing"

	"codex_go/model"
	"codex_go/state"
)

// Rust #54039 lets an async approval return while the redundant review attempt is
// cancelled and drained under the parent's ReviewerTasks tracker, keeps the
// reviewer reservation held until cleanup finishes, and suppresses late evidence
// from the cancelled attempt. The Go Guardian reviewer has none of that
// machinery, so this probe pins the structure behind the Go N-A verdict: one
// Review call makes exactly one model attempt and publishes no verdict before it
// returns, and there is no pool reservation, cancellation token, cached approval
// or task tracker for a concurrent approval to race against.
func TestGuardianReviewHasNoAsyncApprovalRaceLikeRust(t *testing.T) {
	attempts := 0
	reviewer := &modelGuardianReviewer{
		agent: guardianAgentFunc(func(_ context.Context, request *model.AgentRequest) (*model.AgentResponse, error) {
			attempts++
			return &model.AgentResponse{Message: `{"riskLevel":"low","userAuthorization":"high","outcome":"allow","rationale":"ok"}`}, nil
		}),
		store: state.NewReviewStore(),
	}
	decision, _, err := reviewer.Review(context.Background(), "thread-async", "turn-async", "call-async", state.Action{Type: "command", Command: "ls", CWD: "/repo"})
	if err != nil {
		t.Fatalf("Review() error = %v", err)
	}
	if attempts != 1 {
		t.Fatalf("model attempts = %d, want exactly one: Go has no retry loop for an async approval to cancel and drain", attempts)
	}
	if decision != state.DecisionApproved {
		t.Fatalf("decision = %q, want %q", decision, state.DecisionApproved)
	}
}
