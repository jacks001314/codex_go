package appserver

import (
	"context"
	"sync"
	"testing"

	"codex_go/model"
	"codex_go/state"
)

// Rust #62309: an attached reviewer checks the authorization it prepared under
// before and after the verdict. A tightening that lands while the verdict is in
// flight makes that verdict stale, so the review retries under the same deadline
// and only the verdict issued against the current authorization is published.
func TestGuardianReviewPinsServingAuthorizationLikeRust(t *testing.T) {
	cwd := t.TempDir()
	var mu sync.Mutex
	calls := 0
	authorization := state.ServingAuthorization{PolicyFingerprint: "policy-a", RetainedContextComplete: true}
	agent := guardianAgentFunc(func(_ context.Context, _ *model.AgentRequest) (*model.AgentResponse, error) {
		mu.Lock()
		calls++
		if calls == 1 {
			// Authorization tightens while the first verdict is in flight.
			authorization = state.ServingAuthorization{PolicyFingerprint: "policy-b", RetainedContextComplete: true}
		}
		mu.Unlock()
		return &model.AgentResponse{
			ResponseID: "resp",
			Message:    `{"riskLevel":"low","userAuthorization":"high","outcome":"allow","rationale":"ok"}`,
		}, nil
	})
	reviewer := newGuardianActorReviewer(t, agent)
	reviewer.servingAuthorization = func(string, string) state.ServingAuthorization {
		mu.Lock()
		defer mu.Unlock()
		return authorization
	}
	action := state.Action{Type: "command", Command: "echo hi", CWD: cwd}

	decision := reviewGuardianActor(t, reviewer, "thread-root", action)
	if decision != state.DecisionApproved {
		t.Fatalf("decision = %s, want approved after the retry", decision)
	}
	mu.Lock()
	got := calls
	mu.Unlock()
	if got != 2 {
		t.Fatalf("model calls = %d, want the stale verdict retried once", got)
	}
}
