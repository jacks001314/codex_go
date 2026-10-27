package appserver

import (
	"context"
	"testing"
	"time"

	"codex_go/model"
	"codex_go/state"
)

// Rust #56371: a zero configured budget leaves the review without a deadline
// while cancellation and transport timeouts still apply.
func TestGuardianReviewContextUnlimitedLikeRust(t *testing.T) {
	ctx, cancel := guardianReviewContext(context.Background(), 0)
	defer cancel()
	if _, ok := ctx.Deadline(); ok {
		t.Fatal("unlimited review context has a deadline")
	}
	if err := ctx.Err(); err != nil {
		t.Fatalf("unlimited review context error = %v", err)
	}

	limited, cancelLimited := guardianReviewContext(context.Background(), 5*time.Second)
	defer cancelLimited()
	deadline, ok := limited.Deadline()
	if !ok {
		t.Fatal("limited review context has no deadline")
	}
	if remaining := time.Until(deadline); remaining <= 4*time.Second || remaining > 6*time.Second {
		t.Fatalf("review deadline remaining = %v, want about 5s", remaining)
	}
}

// Rust #56371: the resolved per-turn budget replaces the reviewer default.
func TestGuardianReviewerUsesConfiguredTimeoutLikeRust(t *testing.T) {
	reviewer := &modelGuardianReviewer{
		agent: guardianAgentFunc(func(ctx context.Context, _ *model.AgentRequest) (*model.AgentResponse, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		}),
		reviewTimeout: func(string, string) (time.Duration, bool) {
			return time.Millisecond, true
		},
	}
	decision, _, err := reviewer.Review(context.Background(), "thread-1", "turn-1", "call-1", state.Action{Type: "mcp_tool_call", Server: "apps", ToolName: "calendar"})
	if err != nil || decision != state.DecisionTimedOut {
		t.Fatalf("decision=%s err=%v, want timed out", decision, err)
	}
}

// Rust #56371: an unlimited review is not interrupted by the default budget.
func TestGuardianReviewerUnlimitedTimeoutLikeRust(t *testing.T) {
	reviewer := &modelGuardianReviewer{
		timeout: time.Millisecond,
		agent: guardianAgentFunc(func(ctx context.Context, _ *model.AgentRequest) (*model.AgentResponse, error) {
			if _, ok := ctx.Deadline(); ok {
				t.Fatal("unlimited review context kept the reviewer deadline")
			}
			return &model.AgentResponse{Message: `{"riskLevel":"low","userAuthorization":"high","outcome":"allow","rationale":"ok"}`}, nil
		}),
		reviewTimeout: func(string, string) (time.Duration, bool) {
			return 0, false
		},
	}
	decision, _, err := reviewer.Review(context.Background(), "thread-1", "turn-1", "call-1", state.Action{Type: "mcp_tool_call", Server: "apps", ToolName: "calendar"})
	if err != nil || decision != state.DecisionApproved {
		t.Fatalf("decision=%s err=%v, want approved", decision, err)
	}
}
