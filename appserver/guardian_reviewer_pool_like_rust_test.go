package appserver

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"codex_go/model"
	"codex_go/state"
)

// Rust #44252 keeps review orchestration in a ReviewerPool: each reviewed
// thread owns its reviewer session, so concurrently forked threads never
// continue each other's response chain, while a thread's later review reuses
// its own chain.
func TestGuardianReviewerKeepsPerThreadSessionsLikeRust(t *testing.T) {
	cwd := t.TempDir()
	var mu sync.Mutex
	var previous []string
	agent := guardianAgentFunc(func(_ context.Context, request *model.AgentRequest) (*model.AgentResponse, error) {
		mu.Lock()
		previous = append(previous, request.PreviousResponseID)
		id := fmt.Sprintf("resp-%d", len(previous))
		mu.Unlock()
		return &model.AgentResponse{
			ResponseID: id,
			Message:    `{"riskLevel":"low","userAuthorization":"high","outcome":"allow","rationale":"ok"}`,
		}, nil
	})
	reviewer, ok := newModelGuardianReviewer(agent).(*modelGuardianReviewer)
	if !ok {
		t.Fatal("newModelGuardianReviewer() did not return a model reviewer")
	}
	reviewer.reviewPlan = func(string, string) guardianReviewPlan {
		return guardianReviewPlan{Selection: model.ApprovalReviewModel{Model: "gpt-5-guardian"}}
	}
	reviewer.contextMode = func(string, string) state.GuardianContextMode {
		return state.GuardianContextModeThreadOwned
	}

	for _, threadID := range []string{"thread-a", "thread-b", "thread-a"} {
		decision, _, err := reviewer.Review(context.Background(), threadID, "turn-1", "call-1", state.Action{Type: "command", Command: "echo hi", CWD: cwd})
		if err != nil || decision != state.DecisionApproved {
			t.Fatalf("Review(%s) decision = %s err = %v", threadID, decision, err)
		}
	}

	want := []string{"", "", "resp-1"}
	if len(previous) != len(want) {
		t.Fatalf("review parent ids = %#v, want %#v", previous, want)
	}
	for i := range want {
		if previous[i] != want[i] {
			t.Fatalf("review %d parent id = %q, want %q (all: %#v)", i, previous[i], want[i], previous)
		}
	}
}

// The prewarmed trunk session belongs to the first thread that reviews after
// startup; later threads start their own chains instead of continuing it.
func TestGuardianReviewerPrewarmHandsOffToFirstThreadLikeRust(t *testing.T) {
	cwd := t.TempDir()
	agent := &guardianPrewarmAgent{}
	reviewer, ok := newModelGuardianReviewer(agent).(*modelGuardianReviewer)
	if !ok {
		t.Fatal("newModelGuardianReviewer() did not return a model reviewer")
	}
	reviewer.reviewPlan = func(string, string) guardianReviewPlan {
		return guardianReviewPlan{Selection: model.ApprovalReviewModel{Model: "gpt-5-guardian"}}
	}
	reviewer.contextMode = func(string, string) state.GuardianContextMode {
		return state.GuardianContextModeThreadOwned
	}
	if err := reviewer.Prewarm(context.Background()); err != nil {
		t.Fatalf("Prewarm() error = %v", err)
	}
	for _, threadID := range []string{"thread-a", "thread-b"} {
		if _, _, err := reviewer.Review(context.Background(), threadID, "turn-1", "call-1", state.Action{Type: "command", Command: "echo hi", CWD: cwd}); err != nil {
			t.Fatalf("Review(%s) error = %v", threadID, err)
		}
	}

	prewarms, _, requests := agent.snapshot()
	if prewarms != 1 {
		t.Fatalf("prewarms = %d, want 1", prewarms)
	}
	if len(requests) != 2 {
		t.Fatalf("reviews = %d, want 2", len(requests))
	}
	if requests[0].PreviousResponseID != "warm-1" || !requests[0].PreviousResponseFromPrewarm {
		t.Fatalf("first review parent = %q from_prewarm = %v, want warm-1/true", requests[0].PreviousResponseID, requests[0].PreviousResponseFromPrewarm)
	}
	if requests[1].PreviousResponseID != "" || requests[1].PreviousResponseFromPrewarm {
		t.Fatalf("second review parent = %q from_prewarm = %v, want a fresh chain", requests[1].PreviousResponseID, requests[1].PreviousResponseFromPrewarm)
	}
}
