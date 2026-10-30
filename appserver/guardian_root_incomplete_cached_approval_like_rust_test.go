package appserver

import (
	"context"
	"strings"
	"testing"

	"codex_go/model"
	"codex_go/state"
)

// Mirrors Rust #49257: missing retained root instructions stay visible to the
// classifier but do not veto a matching low-risk cached score. Only the
// thread's own retained-context completeness forces a fresh review, so a worker
// with an IncompleteRootInstructions notice can still reuse its local score.
func TestGuardianCachedApprovalAllowsIncompleteRootContextLikeRust(t *testing.T) {
	current := state.ServingAuthorization{
		RetainedContextComplete: true,
		PolicyFingerprint:       "model|policy",
		UserMessageRevision:     3,
	}
	assessment := state.ServingAssessment{Authorization: current}
	if !assessment.AllowsSynchronousReviewSkip(current) {
		t.Fatal("a matching low-risk assessment with complete local context must skip the review")
	}

	// The root omission is review-prompt evidence, not an authorization field.
	notice := state.RootMessage{Kind: state.RootMessageIncompleteRootInstructions}
	if rendered := notice.Render(); !strings.Contains(rendered, "Do not treat the remaining root evidence as complete authorization") {
		t.Fatalf("root omission notice = %q", rendered)
	}

	// A local retained-context omission still requires a fresh review.
	incompleteLocal := current
	incompleteLocal.RetainedContextComplete = false
	if assessment.AllowsSynchronousReviewSkip(incompleteLocal) {
		t.Fatal("incomplete local retained context must force a fresh review")
	}
}

// The reviewer keeps the cached-approval fast path even when the worker's root
// evidence carries the omission notice, because the notice never becomes part of
// the serving authorization.
func TestGuardianReviewUsesCachedApprovalWithIncompleteRootContextLikeRust(t *testing.T) {
	current := state.ServingAuthorization{
		RetainedContextComplete: true,
		PolicyFingerprint:       "model|policy",
		UserMessageRevision:     3,
	}
	reviews := 0
	reviewer := newModelGuardianReviewer(guardianAgentFunc(func(context.Context, *model.AgentRequest) (*model.AgentResponse, error) {
		reviews++
		return &model.AgentResponse{
			Message: `{"riskLevel":"low","userAuthorization":"high","outcome":"allow","rationale":"reviewed"}`,
		}, nil
	})).(*modelGuardianReviewer)
	reviewer.servingAssessment = func(_, _, callID string) *state.ServingAssessment {
		if callID == "call-root-incomplete" {
			return &state.ServingAssessment{Authorization: current}
		}
		return nil
	}
	reviewer.servingAuthorization = func(_, _ string) state.ServingAuthorization { return current }
	reviewer.rootUserAuthorization = func(_, _ string) []state.RootMessage {
		return []state.RootMessage{{Kind: state.RootMessageIncompleteRootInstructions}}
	}

	decision, reason, err := reviewer.Review(
		context.Background(),
		"thread-root-incomplete",
		"turn-root-incomplete",
		"call-root-incomplete",
		state.Action{Type: "command", Command: "ls", CWD: "/tmp"},
	)
	if err != nil {
		t.Fatalf("Review() error = %v", err)
	}
	if decision != state.DecisionApproved || reason != "serving assessment" {
		t.Fatalf("Review() = %q, %q; want the retained low-risk assessment", decision, reason)
	}
	if reviews != 0 {
		t.Fatalf("full reviews = %d, want 0", reviews)
	}
}
