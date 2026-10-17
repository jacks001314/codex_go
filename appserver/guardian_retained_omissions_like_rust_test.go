package appserver

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"codex_go/model"
	"codex_go/retainedctx"
	"codex_go/state"
)

func retainedContextFixture(t *testing.T, raw string) *retainedctx.RetainedContext {
	t.Helper()
	var context retainedctx.RetainedContext
	if err := json.Unmarshal([]byte(raw), &context); err != nil {
		t.Fatalf("retained context fixture did not parse: %v", err)
	}
	return &context
}

// Mirrors Rust #49796 end-to-end: the first review delivers the retained
// omission notice, an unchanged continuation suppresses the repeated
// notice-only section, and a later complete snapshot emits one
// availability-change notice.
func TestGuardianReviewDeduplicatesRetainedOmissionNoticesLikeRust(t *testing.T) {
	incomplete := retainedContextFixture(t, `{"verified_answers":[],"incomplete":false,"user_messages":[],"user_messages_incomplete":true,"assistant_messages":[],"assistant_messages_incomplete":false,"next_order":0}`)
	complete := retainedContextFixture(t, `{"verified_answers":[],"incomplete":false,"user_messages":[],"user_messages_incomplete":false,"assistant_messages":[],"assistant_messages_incomplete":false,"next_order":0}`)
	current := incomplete
	var prompts []string
	reviewer := &modelGuardianReviewer{
		agent: guardianAgentFunc(func(_ context.Context, request *model.AgentRequest) (*model.AgentResponse, error) {
			prompts = append(prompts, request.Prompt)
			return &model.AgentResponse{Message: guardianDecisionDurationAssessment}, nil
		}),
		store: state.NewReviewStore(),
		retainedContext: func(string, string) *retainedctx.RetainedContext {
			return current
		},
	}
	for index := 0; index < 3; index++ {
		decision, _, err := reviewer.Review(context.Background(), "thread-retained-omissions", "turn", "call", state.Action{Type: "command", Command: "ls", CWD: t.TempDir()})
		if err != nil || decision != state.DecisionApproved {
			t.Fatalf("review %d = %q, %v; want approved", index, decision, err)
		}
		if index == 0 {
			current = incomplete
		}
		if index == 1 {
			current = complete
		}
	}
	if len(prompts) != 3 {
		t.Fatalf("prompts = %d, want 3", len(prompts))
	}
	if !strings.Contains(prompts[0], "some retained user instructions are unavailable") {
		t.Fatalf("first prompt = %q, want the retained omission notice", prompts[0])
	}
	if strings.Contains(prompts[1], "some retained user instructions are unavailable") {
		t.Fatalf("continuation prompt repeated the omission notice: %q", prompts[1])
	}
	if !strings.Contains(prompts[2], "retained source availability has changed") {
		t.Fatalf("recovery prompt = %q, want the availability-change notice", prompts[2])
	}
}
