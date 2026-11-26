package appserver

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"codex_go/model"
	"codex_go/state"
)

// Rust parity: codex-rs/ext/guardian-v3/src/actor.rs + conversation.rs (#62134).
// Rust gives every acting agent with a board identity its own ActorReviewer
// slot: one reservation at a time, the verdict conversation returns to the slot
// only for SessionDisposition::Reusable, and a dropped reservation retires its
// reviewer. Go's Guardian reviewer conversation is host-owned per acting thread,
// so the same contract is: one reviewer chain per agent, reuse only after a
// completed review, a discarded or failed verdict starts a fresh chain, and a
// changed reviewer binding or a stopped thread retires it.
func TestGuardianActorReviewerKeepsOneConversationPerAgentLikeRust(t *testing.T) {
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
	reviewer := newGuardianActorReviewer(t, agent)
	action := state.Action{Type: "command", Command: "echo hi", CWD: cwd}

	// Root agent and one subagent thread each own a reviewer conversation, and
	// a completed verdict returns it to the agent's slot for reuse.
	reviewGuardianActor(t, reviewer, "thread-root", action)
	reviewGuardianActor(t, reviewer, "thread-child", action)
	reviewGuardianActor(t, reviewer, "thread-root", action)
	mu.Lock()
	first := append([]string(nil), parents...)
	mu.Unlock()
	want := []string{"", "", "resp-1"}
	for i := range want {
		if first[i] != want[i] {
			t.Fatalf("review parents = %#v, want %#v", first, want)
		}
	}
}

func newGuardianActorReviewer(t *testing.T, agent model.AgentRunner) *modelGuardianReviewer {
	t.Helper()
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
	return reviewer
}

func reviewGuardianActor(t *testing.T, reviewer *modelGuardianReviewer, threadID string, action state.Action) state.ReviewDecision {
	t.Helper()
	decision, _, err := reviewer.Review(context.Background(), threadID, "turn-1", "call-1", action)
	if err != nil {
		t.Fatalf("Review(%s) error = %v", threadID, err)
	}
	return decision
}

// Rust #62134: a reservation that never finishes with Reusable is dropped, so
// the agent's next verdict starts a new reviewer thread instead of continuing
// the failed one. Go discards the chain when a verdict fails, and a fresh chain
// must not inherit the failed attempt's response id.
func TestGuardianActorDropsFailedReviewerConversationLikeRust(t *testing.T) {
	cwd := t.TempDir()
	var mu sync.Mutex
	var parents []string
	failNext := false
	agent := guardianAgentFunc(func(_ context.Context, request *model.AgentRequest) (*model.AgentResponse, error) {
		mu.Lock()
		parents = append(parents, request.PreviousResponseID)
		attempt := len(parents)
		fail := failNext
		failNext = false
		mu.Unlock()
		if fail {
			return nil, errors.New("guardian inference failed")
		}
		return &model.AgentResponse{
			ResponseID: fmt.Sprintf("resp-%d", attempt),
			Message:    `{"riskLevel":"low","userAuthorization":"high","outcome":"allow","rationale":"ok"}`,
		}, nil
	})
	reviewer := newGuardianActorReviewer(t, agent)
	action := state.Action{Type: "command", Command: "echo hi", CWD: cwd}

	reviewGuardianActor(t, reviewer, "thread-root", action)
	mu.Lock()
	failNext = true
	mu.Unlock()
	// A failed inference fails closed into a denial; the point here is that the
	// attempt is not retained as the reusable verdict.
	if decision, _, _ := reviewer.Review(context.Background(), "thread-root", "turn-1", "call-1", action); decision == state.DecisionApproved {
		t.Fatal("failed verdict was approved")
	}
	reviewGuardianActor(t, reviewer, "thread-root", action)

	mu.Lock()
	got := append([]string(nil), parents...)
	mu.Unlock()
	// Rust guardian-reviewer SessionDisposition::Rollback keeps the pool trunk and
	// continues from the last committed review, so this attempt's response id is
	// never the next base. Rust #62134 retires the actor's reviewer thread on a
	// non-reusable disposition; Go's reviewer conversation is the pool trunk, so
	// the equivalent guarantee is that the failed attempt is not reused.
	want := []string{"", "resp-1", "resp-1"}
	if len(got) != len(want) {
		t.Fatalf("review parents = %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("review %d parent = %q, want %q (all: %#v)", i, got[i], want[i], got)
		}
	}
}

// Rust #62134: reuse requires matching reviewer settings. Rebinding the host
// Guardian provider retires every per-agent conversation, so the next verdict
// starts with empty history on the new runner instead of continuing the
// previous provider chain.
func TestGuardianActorRebindingProviderRetiresConversationsLikeRust(t *testing.T) {
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
	reviewer := newGuardianActorReviewer(t, agent)
	action := state.Action{Type: "command", Command: "echo hi", CWD: cwd}

	reviewGuardianActor(t, reviewer, "thread-root", action)
	reviewGuardianActor(t, reviewer, "thread-root", action)

	var reboundParents []string
	reviewer.useGuardianAgent(guardianAgentFunc(func(_ context.Context, request *model.AgentRequest) (*model.AgentResponse, error) {
		mu.Lock()
		reboundParents = append(reboundParents, request.PreviousResponseID)
		mu.Unlock()
		return &model.AgentResponse{ResponseID: "provider-2", Message: `{"riskLevel":"low","userAuthorization":"high","outcome":"allow","rationale":"ok"}`}, nil
	}))
	reviewGuardianActor(t, reviewer, "thread-root", action)

	mu.Lock()
	got := append([]string(nil), parents...)
	bound := append([]string(nil), reboundParents...)
	mu.Unlock()
	want := []string{"", "resp-1"}
	for i := range want {
		if i >= len(got) || got[i] != want[i] {
			t.Fatalf("actor review parents = %#v, want %#v", got, want)
		}
	}
	if len(bound) != 1 || bound[0] != "" {
		t.Fatalf("rebound provider review parents = %#v, want one empty chain", bound)
	}
}
