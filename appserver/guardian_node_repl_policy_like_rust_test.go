package appserver

import (
	"context"
	"testing"

	"codex_go/model"
	"codex_go/state"
)

// Mirrors Rust #41919: the effective Node REPL policy is part of session reuse.
// A change invalidates the cached reviewer session; an explicitly empty policy
// is distinct from an unset one.
func TestGuardianSessionInvalidatesOnNodeReplPolicyChangeLikeRust(t *testing.T) {
	runner := &guardianSessionRunner{previous: "resp-1", seeded: "seed-1"}
	runner.EnsureReviewPolicy("bundled")
	if runner.previous != "resp-1" || runner.seeded != "seed-1" {
		t.Fatalf("first policy initialization cleared the session: %#v", runner)
	}
	runner.EnsureReviewPolicy("bundled")
	if runner.previous != "resp-1" || runner.seeded != "seed-1" {
		t.Fatalf("unchanged policy cleared the session: %#v", runner)
	}
	runner.EnsureReviewPolicy("")
	if runner.previous != "" || runner.seeded != "" {
		t.Fatalf("changed policy kept the cached session: %#v", runner)
	}

	empty := &guardianSessionRunner{}
	empty.EnsureReviewPolicy("")
	empty.previous = "resp-empty"
	empty.EnsureReviewPolicy("")
	if empty.previous != "resp-empty" {
		t.Fatalf("repeated empty policy cleared the session: %#v", empty)
	}
}

// Mirrors Rust #41919 end-to-end: same-policy reviews reuse the previous
// response, while a policy change starts a fresh reviewer session.
func TestGuardianReviewReusesOrInvalidatesSessionByNodeReplPolicyLikeRust(t *testing.T) {
	policy := "bundled"
	var previousIDs []string
	runner := &guardianSessionRunner{agent: guardianAgentFunc(func(_ context.Context, request *model.AgentRequest) (*model.AgentResponse, error) {
		previousIDs = append(previousIDs, request.PreviousResponseID)
		return &model.AgentResponse{ResponseID: "resp-1", Message: guardianDecisionDurationAssessment}, nil
	})}
	reviewer := &modelGuardianReviewer{
		agent: runner,
		store: state.NewReviewStore(),
		reviewPlan: func(string, string) guardianReviewPlan {
			return guardianReviewPlan{NodeReplPolicy: policy}
		},
	}
	action := state.Action{Type: "command", Command: "ls", CWD: t.TempDir()}
	for index := 0; index < 2; index++ {
		if _, _, err := reviewer.Review(context.Background(), "thread-policy", "turn-policy", "call-policy", action); err != nil {
			t.Fatalf("review %d error = %v", index, err)
		}
	}
	policy = "changed"
	if _, _, err := reviewer.Review(context.Background(), "thread-policy", "turn-policy", "call-policy", action); err != nil {
		t.Fatalf("changed-policy review error = %v", err)
	}
	want := []string{"", "resp-1", ""}
	if len(previousIDs) != len(want) {
		t.Fatalf("previous ids = %#v, want %v", previousIDs, want)
	}
	for index := range want {
		if previousIDs[index] != want[index] {
			t.Fatalf("previous ids = %#v, want %v", previousIDs, want)
		}
	}
}
