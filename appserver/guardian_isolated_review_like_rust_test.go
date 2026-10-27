package appserver

import (
	"context"
	"testing"

	"codex_go/model"
)

// Mirror of Rust's ReviewerPool::review_isolated (#57522): an isolated review
// gets no inherited snapshot, runs in a separate session, and leaves the
// prewarmed session for the next regular review.
func TestGuardianIsolatedReviewPreservesPrewarmedSessionLikeRust(t *testing.T) {
	agent := &guardianPrewarmAgent{}
	session := &guardianSessionRunner{agent: agent}
	if err := session.Prewarm(context.Background()); err != nil {
		t.Fatalf("Prewarm() error = %v", err)
	}
	if _, err := session.RunIsolated(context.Background(), &model.AgentRequest{
		PreviousResponseID: "caller-seed",
	}); err != nil {
		t.Fatalf("RunIsolated() error = %v", err)
	}
	if _, err := session.Run(context.Background(), &model.AgentRequest{}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	agent.mu.Lock()
	defer agent.mu.Unlock()
	if len(agent.requests) != 2 {
		t.Fatalf("requests = %d, want 2", len(agent.requests))
	}
	if got := agent.requests[0].PreviousResponseID; got != "" {
		t.Fatalf("isolated review inherited snapshot %q, want none", got)
	}
	if got := agent.requests[1].PreviousResponseID; got != "warm-1" {
		t.Fatalf("regular review previous response = %q, want prewarmed session", got)
	}
	if agent.websocketRuns != 1 {
		t.Fatalf("websocket runs = %d, want only the reusable review", agent.websocketRuns)
	}
}
