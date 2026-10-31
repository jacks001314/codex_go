package appserver

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"codex_go/model"
	"codex_go/state"
)

// Rust parity: codex-rs/ext/guardian-v2/src/sync_reviewer/mod.rs `install`
// registers Guardian for both thread and turn lifecycles, and
// `ThreadLifecycleContributor::on_thread_stop` shuts the reviewer pool down
// (#45672). Go's reusable reviewer is host-owned, so the thread's closed
// runtime is the ownership boundary: unloading one thread drops only that
// thread's response chain.
func TestGuardianReviewerDropsThreadSessionOnUnloadLikeRust(t *testing.T) {
	cwd := t.TempDir()
	var mu sync.Mutex
	var parents []string
	agent := guardianAgentFunc(func(_ context.Context, request *model.AgentRequest) (*model.AgentResponse, error) {
		mu.Lock()
		parents = append(parents, request.PreviousResponseID)
		id := fmt.Sprintf("resp-%d", len(parents))
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
	router := NewRuntimeRouter(RuntimeServices{GuardianReviewer: reviewer})

	review := func(threadID string) {
		t.Helper()
		decision, _, err := reviewer.Review(context.Background(), threadID, "turn-1", "call-1", state.Action{Type: "command", Command: "echo hi", CWD: cwd})
		if err != nil || decision != state.DecisionApproved {
			t.Fatalf("Review(%s) decision = %s err = %v", threadID, decision, err)
		}
	}

	review("thread-a")
	review("thread-b")
	review("thread-a")
	if reviewer.existingSessionFor("thread-a") == nil || reviewer.existingSessionFor("thread-b") == nil {
		t.Fatal("reviews did not create per-thread reviewer sessions")
	}

	router.markThreadUnloaded("thread-a")
	if reviewer.existingSessionFor("thread-a") != nil {
		t.Fatal("unloaded thread kept its reusable reviewer")
	}
	if reviewer.existingSessionFor("thread-b") == nil {
		t.Fatal("unloading thread-a dropped thread-b's reviewer")
	}

	review("thread-a")
	review("thread-b")

	want := []string{"", "", "resp-1", "", "resp-2"}
	if len(parents) != len(want) {
		t.Fatalf("review parent ids = %#v, want %#v", parents, want)
	}
	for i := range want {
		if parents[i] != want[i] {
			t.Fatalf("review %d parent id = %q, want %q (all: %#v)", i, parents[i], want[i], parents)
		}
	}
}

// Rust parity: codex-rs/ext/guardian-reviewer/src/pool.rs `ReviewerPool`
// replaces a cancelled trunk before reuse, and a dropped review never becomes
// the reuse base (#45672). Go's reviewer replays the parent transcript per
// review, so the equivalent contract is that a cancelled review rolls the
// response cursor back to the last committed review.
func TestGuardianCancelledReviewKeepsCommittedCursorLikeRust(t *testing.T) {
	cwd := t.TempDir()
	var mu sync.Mutex
	var parents []string
	blocked := make(chan struct{})
	agent := guardianAgentFunc(func(ctx context.Context, request *model.AgentRequest) (*model.AgentResponse, error) {
		mu.Lock()
		parents = append(parents, request.PreviousResponseID)
		attempt := len(parents)
		mu.Unlock()
		if attempt == 2 {
			close(blocked)
			<-ctx.Done()
			return nil, ctx.Err()
		}
		return &model.AgentResponse{
			ResponseID: fmt.Sprintf("resp-%d", attempt),
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

	action := state.Action{Type: "command", Command: "echo hi", CWD: cwd}
	if decision, _, err := reviewer.Review(context.Background(), "thread-a", "turn-1", "call-1", action); err != nil || decision != state.DecisionApproved {
		t.Fatalf("first review decision = %s err = %v", decision, err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancelled := make(chan state.ReviewDecision, 1)
	go func() {
		decision, _, _ := reviewer.Review(ctx, "thread-a", "turn-1", "call-1", action)
		cancelled <- decision
	}()
	select {
	case <-blocked:
	case <-time.After(5 * time.Second):
		cancel()
		t.Fatal("cancelled review never reached the agent")
	}
	cancel()
	select {
	case decision := <-cancelled:
		if decision != state.DecisionAborted {
			t.Fatalf("cancelled review decision = %s, want aborted", decision)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancelled review did not return")
	}

	if decision, _, err := reviewer.Review(context.Background(), "thread-a", "turn-1", "call-1", action); err != nil || decision != state.DecisionApproved {
		t.Fatalf("third review decision = %s err = %v", decision, err)
	}
	mu.Lock()
	got := append([]string(nil), parents...)
	mu.Unlock()
	// The cancelled attempt never reaches the agent's committed response, so the
	// next review continues from the last committed cursor (resp-1) instead of
	// inheriting the aborted attempt.
	want := []string{"", "resp-1", "resp-1"}
	if len(got) != len(want) {
		t.Fatalf("review parent ids = %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("review %d parent id = %q, want %q (all: %#v)", i, got[i], want[i], got)
		}
	}
}

// Lifecycle resets must not create reviewer state for threads that never
// reviewed: the reviewer is per-thread runtime state, not a cache filled by
// history events.
func TestGuardianResetDoesNotCreateReviewerSessionLikeRust(t *testing.T) {
	reviewer, ok := newModelGuardianReviewer(guardianAgentFunc(func(context.Context, *model.AgentRequest) (*model.AgentResponse, error) {
		return &model.AgentResponse{ResponseID: "resp-1", Message: `{"riskLevel":"low","userAuthorization":"high","outcome":"allow","rationale":"ok"}`}, nil
	})).(*modelGuardianReviewer)
	if !ok {
		t.Fatal("newModelGuardianReviewer() did not return a model reviewer")
	}
	router := NewRuntimeRouter(RuntimeServices{GuardianReviewer: reviewer})
	router.resetGuardianAfterParentHistoryReset("thread-never-reviewed")
	if reviewer.existingSessionFor("thread-never-reviewed") != nil {
		t.Fatal("history reset created a reviewer session for a thread that never reviewed")
	}
}
