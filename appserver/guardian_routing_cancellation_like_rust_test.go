package appserver

import (
	"context"
	"errors"
	"testing"

	"codex_go/model"
	"codex_go/state"
)

// TestGuardianApprovalRoutingHonorsCancellationBeforeRoutingLikeRust mirrors
// Rust #45693 (guardian-reviewer/src/routing.rs ReviewRequest::decide): the
// routing step checks cancellation before any fast decision, so a cancelled
// approval operation reports Abort instead of an approval that no longer has a
// parent turn to service.
func TestGuardianApprovalRoutingHonorsCancellationBeforeRoutingLikeRust(t *testing.T) {
	agentCalls := 0
	agent := guardianAgentFunc(func(context.Context, *model.AgentRequest) (*model.AgentResponse, error) {
		agentCalls++
		return nil, errors.New("routing must not reach the model")
	})
	newReviewer := func() (*modelGuardianReviewer, *int) {
		fastDecisions := 0
		reviewer := &modelGuardianReviewer{
			agent:        agent,
			store:        state.NewReviewStore(),
			breaker:      state.NewCircuitBreaker(),
			fullAccess:   func(string, string) bool { return true },
			fastDecision: func(context.Context, string, string, string) { fastDecisions++ },
			notify:       func(string, *state.Event) {},
		}
		return reviewer, &fastDecisions
	}

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	reviewer, fastDecisions := newReviewer()
	decision, _, err := reviewer.Review(cancelled, "thread-routing-cancel", "turn-routing-cancel", "call-routing-cancel", state.Action{Type: "mcp_tool_call"})
	if err != nil {
		t.Fatalf("cancelled routing error = %v, want nil", err)
	}
	if decision != state.DecisionAborted {
		t.Fatalf("cancelled routing decision = %s, want %s", decision, state.DecisionAborted)
	}
	if *fastDecisions != 0 {
		t.Fatalf("cancelled routing ran %d fast decisions, want 0", *fastDecisions)
	}
	if agentCalls != 0 {
		t.Fatalf("cancelled routing reached the model %d times, want 0", agentCalls)
	}

	reviewer, fastDecisions = newReviewer()
	decision, _, err = reviewer.Review(context.Background(), "thread-routing-live", "turn-routing-live", "call-routing-live", state.Action{Type: "mcp_tool_call"})
	if err != nil {
		t.Fatalf("live routing error = %v, want nil", err)
	}
	if decision != state.DecisionApproved {
		t.Fatalf("live routing decision = %s, want %s", decision, state.DecisionApproved)
	}
	if *fastDecisions != 1 {
		t.Fatalf("live routing ran %d fast decisions, want 1", *fastDecisions)
	}
}
