package appserver

import (
	"context"
	"errors"
	"testing"

	"codex_go/model"
	"codex_go/state"
)

// Rust #54264 (ext/guardian-reviewer/src/conversation.rs): a failed review that
// retained its input still admits the transcript cursor, so the next review
// reports prior review context, but it never counts as a completed review.
func TestGuardianSessionAdmitsTranscriptWithoutCompletingFailedReviewLikeRust(t *testing.T) {
	runner := &guardianSessionRunner{
		agent: guardianAgentFunc(func(context.Context, *model.AgentRequest) (*model.AgentResponse, error) {
			return nil, errors.New("guardian inference failed")
		}),
	}
	reviewer := &modelGuardianReviewer{agent: runner, store: state.NewReviewStore()}
	decision, _, err := reviewer.Review(context.Background(), "thread-admit", "turn-admit", "call-admit", state.Action{Type: "command", Command: "ls", CWD: t.TempDir()})
	if err != nil {
		t.Fatalf("review error = %v", err)
	}
	if decision != state.DecisionDenied {
		t.Fatalf("decision = %q, want failed_closed denial", decision)
	}
	kind, hadPrior := runner.reviewSessionAttribution()
	if hadPrior == nil || !*hadPrior {
		t.Fatalf("had prior review context = %v, want true after admission", hadPrior)
	}
	if kind != "trunk_new" {
		t.Fatalf("session kind = %q, want trunk_new", kind)
	}
	if got := runner.completedReviewCount(); got != 0 {
		t.Fatalf("completed reviews = %d, want 0: a failed review must not complete", got)
	}

	// The next review starts from the admitted transcript and, once it
	// completes, bumps the completed-review count exactly once.
	runner.agent = guardianAgentFunc(func(context.Context, *model.AgentRequest) (*model.AgentResponse, error) {
		return &model.AgentResponse{Message: guardianDecisionDurationAssessment}, nil
	})
	decision, _, err = reviewer.Review(context.Background(), "thread-admit", "turn-admit-2", "call-admit-2", state.Action{Type: "command", Command: "ls", CWD: t.TempDir()})
	if err != nil {
		t.Fatalf("second review error = %v", err)
	}
	if decision != state.DecisionApproved {
		t.Fatalf("second decision = %q, want approved", decision)
	}
	_, hadPrior = runner.reviewSessionAttribution()
	if hadPrior == nil || !*hadPrior {
		t.Fatalf("second had prior review context = %v, want true", hadPrior)
	}
	if got := runner.completedReviewCount(); got != 1 {
		t.Fatalf("completed reviews = %d, want 1", got)
	}
}

// Rust #54264: a completed review advances both the cursor and the completed
// count, which is what distinguishes it from a failed-but-admitted review.
func TestGuardianSessionCompletesSuccessfulReviewLikeRust(t *testing.T) {
	runner := &guardianSessionRunner{
		agent: guardianAgentFunc(func(context.Context, *model.AgentRequest) (*model.AgentResponse, error) {
			return &model.AgentResponse{Message: guardianDecisionDurationAssessment}, nil
		}),
	}
	reviewer := &modelGuardianReviewer{agent: runner, store: state.NewReviewStore()}
	if _, _, err := reviewer.Review(context.Background(), "thread-complete", "turn-complete", "call-complete", state.Action{Type: "command", Command: "ls", CWD: t.TempDir()}); err != nil {
		t.Fatalf("review error = %v", err)
	}
	if got := runner.completedReviewCount(); got != 1 {
		t.Fatalf("completed reviews = %d, want 1", got)
	}
	_, hadPrior := runner.reviewSessionAttribution()
	if hadPrior == nil || !*hadPrior {
		t.Fatalf("had prior review context = %v, want true", hadPrior)
	}
}
