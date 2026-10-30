package appserver

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"codex_go/model"
	"codex_go/state"
)

type guardianIsolatedAgent struct {
	isolated func(context.Context, *model.AgentRequest) (*model.AgentResponse, error)
	regular  func(context.Context, *model.AgentRequest) (*model.AgentResponse, error)
}

func (a *guardianIsolatedAgent) Run(ctx context.Context, request *model.AgentRequest) (*model.AgentResponse, error) {
	if a.regular == nil {
		return nil, errors.New("regular Run called for an isolated assessment")
	}
	return a.regular(ctx, request)
}

func (a *guardianIsolatedAgent) RunIsolated(ctx context.Context, request *model.AgentRequest) (*model.AgentResponse, error) {
	return a.isolated(ctx, request)
}

// Mirrors Rust #57834: an isolated assessment uses the captured parent history
// (not the live thread), runs in the isolated reviewer, and returns the parsed
// assessment without publishing an approval.
func TestGuardianAssessIsolatedUsesCapturedHistoryLikeRust(t *testing.T) {
	var captured *model.AgentRequest
	agent := &guardianIsolatedAgent{isolated: func(_ context.Context, request *model.AgentRequest) (*model.AgentResponse, error) {
		captured = request
		return &model.AgentResponse{Message: `{"riskLevel":"low","userAuthorization":"high","outcome":"allow","rationale":"ok"}`}, nil
	}}
	reviewer := &modelGuardianReviewer{
		agent: agent,
		store: state.NewReviewStore(),
		// The live transcript now contains an instruction recorded after the
		// action was captured; the isolated reviewer must not see it.
		transcript: func(string) []string { return []string{"Instruction added after capture."} },
	}
	input := guardianIsolatedReviewInput{
		ThreadID: "thread-isolated",
		TurnID:   "turn-isolated",
		Action:   state.Action{Type: "command", Command: "git push", CWD: "/tmp"},
		TranscriptEntries: []state.GuardianTranscriptEntry{
			{Author: "user", Text: "Please check the repo visibility"},
			{Author: "assistant", Text: "Need to push the reviewed docs fix."},
		},
	}
	assessment, err := reviewer.AssessIsolated(context.Background(), input, 0)
	if err != nil {
		t.Fatalf("AssessIsolated() error = %v", err)
	}
	if assessment == nil || assessment.Outcome != state.OutcomeAllow {
		t.Fatalf("assessment = %#v", assessment)
	}
	if captured == nil {
		t.Fatal("isolated reviewer was not called")
	}
	if !strings.Contains(captured.Prompt, "Please check the repo visibility") ||
		!strings.Contains(captured.Prompt, "Need to push the reviewed docs fix.") {
		t.Fatalf("prompt missing captured history:\n%s", captured.Prompt)
	}
	if strings.Contains(captured.Prompt, "Instruction added after capture.") {
		t.Fatalf("prompt leaked history recorded after capture:\n%s", captured.Prompt)
	}
	if captured.TaskKind != model.AgentTaskReview || !strings.EqualFold(captured.Originator, "guardian") {
		t.Fatalf("isolated request kind=%q originator=%q", captured.TaskKind, captured.Originator)
	}
}

// Rust #57834 honors the review deadline and cancellation of an isolated
// assessment; neither may surface as a completed assessment.
func TestGuardianAssessIsolatedHonorsDeadlineAndCancellationLikeRust(t *testing.T) {
	blocking := &guardianIsolatedAgent{isolated: func(ctx context.Context, _ *model.AgentRequest) (*model.AgentResponse, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}}
	reviewer := &modelGuardianReviewer{agent: blocking, store: state.NewReviewStore()}
	input := guardianIsolatedReviewInput{
		ThreadID: "thread-isolated-deadline",
		TurnID:   "turn-isolated-deadline",
		Action:   state.Action{Type: "command", Command: "ls", CWD: "/tmp"},
	}
	if _, err := reviewer.AssessIsolated(context.Background(), input, 25*time.Millisecond); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("deadline error = %v, want context.DeadlineExceeded", err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := reviewer.AssessIsolated(canceled, input, 0); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation error = %v, want context.Canceled", err)
	}
}

// A response without a valid assessment is a review error, never a decision.
func TestGuardianAssessIsolatedRejectsMissingAssessmentLikeRust(t *testing.T) {
	agent := &guardianIsolatedAgent{isolated: func(context.Context, *model.AgentRequest) (*model.AgentResponse, error) {
		return &model.AgentResponse{Message: "not json"}, nil
	}}
	reviewer := &modelGuardianReviewer{agent: agent, store: state.NewReviewStore()}
	if _, err := reviewer.AssessIsolated(context.Background(), guardianIsolatedReviewInput{
		ThreadID: "thread-isolated-bad",
		TurnID:   "turn-isolated-bad",
		Action:   state.Action{Type: "command", Command: "ls", CWD: "/tmp"},
	}, 0); err == nil {
		t.Fatal("AssessIsolated() accepted a response without an assessment")
	}
}
