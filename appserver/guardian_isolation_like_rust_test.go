package appserver

import (
	"context"
	"testing"

	"codex_go/model"
	"codex_go/sandbox"
	"codex_go/state"
)

// TestGuardianReviewRequestIsIsolatedFromParentToolsLikeRust pins the Go
// equivalent of Rust #40497/#44521. Go does not spawn a managed reviewer
// thread, so isolation is enforced at the review-request boundary: the request
// carries the reviewer policy/model and permission evidence, but never the
// parent's tool catalog, MCP bindings, extensions, or subagent attribution.
func TestGuardianReviewRequestIsIsolatedFromParentToolsLikeRust(t *testing.T) {
	var captured *model.AgentRequest
	profile := sandbox.ReadOnlyPermissionProfile()
	reviewer := &modelGuardianReviewer{
		agent: guardianAgentFunc(func(_ context.Context, request *model.AgentRequest) (*model.AgentResponse, error) {
			clone := *request
			clone.Tools = append([]any(nil), request.Tools...)
			clone.InputItems = append([]any(nil), request.InputItems...)
			captured = &clone
			return &model.AgentResponse{Message: `{"riskLevel":"low","userAuthorization":"high","outcome":"allow","rationale":"isolated"}`}, nil
		}),
		store: state.NewReviewStore(),
		reviewPlan: func(string, string) guardianReviewPlan {
			return guardianReviewPlan{
				Selection: model.ApprovalReviewModel{
					Model: "codex-auto-review",
				},
				Instructions: "Review only the supplied action.",
			}
		},
		permissionProfile: func(string, string) *sandbox.PermissionProfile {
			value := profile
			return &value
		},
	}

	decision, _, err := reviewer.Review(
		context.Background(),
		"thread-root",
		"turn-root",
		"call-root",
		state.Action{Type: "mcp_tool_call", Server: "parent_mcp", ToolName: "write", CWD: "/repo"},
	)
	if err != nil {
		t.Fatalf("Review() error = %v", err)
	}
	if decision != state.DecisionApproved {
		t.Fatalf("decision = %q, want approved", decision)
	}
	if captured == nil {
		t.Fatal("Guardian review request was not captured")
	}
	if len(captured.Tools) != 0 {
		t.Fatalf("Guardian review inherited parent tools: %#v", captured.Tools)
	}
	if captured.TaskKind != model.AgentTaskReview || captured.Originator != "guardian" {
		t.Fatalf("Guardian review attribution = %q/%q", captured.TaskKind, captured.Originator)
	}
	if captured.ClientMetadata["x-openai-subagent"] != "guardian" {
		t.Fatalf("Guardian subagent metadata = %#v", captured.ClientMetadata)
	}
	if captured.PermissionProfile == nil || captured.PermissionProfile.SandboxPolicy == nil ||
		captured.PermissionProfile.SandboxPolicy.Kind != sandbox.SandboxReadOnly {
		t.Fatalf("Guardian permission evidence = %#v, want read-only", captured.PermissionProfile)
	}
}
