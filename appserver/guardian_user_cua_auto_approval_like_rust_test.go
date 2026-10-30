package appserver

import (
	"context"
	"reflect"
	"testing"

	"codex_go/mcp"
	"codex_go/sandbox"
	"codex_go/state"
)

func userNodeReplElicitation(server string, meta map[string]any) *mcp.MCPElicitationRequest {
	return &mcp.MCPElicitationRequest{
		ServerName:      server,
		ThreadID:        "thread-user-cua",
		TurnID:          "turn-user-cua",
		ElicitationID:   "call-user-cua",
		Method:          "elicitation/create",
		RequestedSchema: map[string]any{"type": "object", "properties": map[string]any{}},
		Meta:            meta,
	}
}

func userNodeReplElicitationMeta() map[string]any {
	return map[string]any{
		"codex_request_type":  "approval_request",
		"codex_approval_kind": "mcp_tool_call",
		"tool_name":           "js",
		"connector_id":        "node_repl",
		"tool_params":         map[string]any{"code": "1 + 1"},
	}
}

func userCUAHandler(authority mcpElicitationAuthority, reviewer GuardianReviewer) *appserverMCPElicitationHandler {
	return &appserverMCPElicitationHandler{
		reviewer: reviewer,
		authority: func(string, string, string) mcpElicitationAuthority {
			return authority
		},
	}
}

func userCUAAuthority(reviewer string) mcpElicitationAuthority {
	return mcpElicitationAuthority{
		ApprovalPolicy:        sandbox.ApprovalOnRequest,
		ApprovalsReviewer:     reviewer,
		AllowsMCPElicitations: true,
	}
}

// TestUserApprovalAutoAcceptsOrdinaryNodeReplExecutionLikeRust covers Rust
// #42256: in User approval mode an ordinary node_repl.js execution confirmation
// is accepted without sampling or prompting the user.
func TestUserApprovalAutoAcceptsOrdinaryNodeReplExecutionLikeRust(t *testing.T) {
	for _, server := range []string{"node_repl", "cua_repl"} {
		t.Run(server, func(t *testing.T) {
			reviewerCalled := false
			reviewer := guardianReviewerFunc(func(context.Context, string, string, string, state.Action) (state.ReviewDecision, string, error) {
				reviewerCalled = true
				return state.DecisionDenied, "must not review ordinary execution", nil
			})
			handler := userCUAHandler(userCUAAuthority("user"), reviewer)

			response, err := handler.HandleMCPElicitation(context.Background(), userNodeReplElicitation(server, userNodeReplElicitationMeta()))
			if err != nil {
				t.Fatalf("HandleMCPElicitation() error = %v", err)
			}
			if response == nil || response.Action != mcp.MCPElicitationActionAccept {
				t.Fatalf("response = %#v, want accept", response)
			}
			if !reflect.DeepEqual(response.Content, map[string]any{}) {
				t.Fatalf("content = %#v, want an empty object", response.Content)
			}
			meta, _ := response.Meta.(map[string]any)
			if meta["approvals_reviewer"] != "auto_review" {
				t.Fatalf("meta = %#v, want approvals_reviewer auto_review", response.Meta)
			}
			if reviewerCalled {
				t.Fatal("ordinary node_repl execution reached the Guardian reviewer in User approval mode")
			}
		})
	}
}

// TestUserApprovalKeepsSensitiveAndInteractiveNodeReplChecksLikeRust ensures
// Rust #42256 only skips ordinary execution confirmations.
func TestUserApprovalKeepsSensitiveAndInteractiveNodeReplChecksLikeRust(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*mcp.MCPElicitationRequest)
	}{
		{
			name: "sensitive action",
			mutate: func(request *mcp.MCPElicitationRequest) {
				request.Meta.(map[string]any)["codex_sensitive_action"] = true
			},
		},
		{
			name: "requires user input",
			mutate: func(request *mcp.MCPElicitationRequest) {
				request.Meta.(map[string]any)["codex_requires_user_input"] = true
			},
		},
		{
			name: "different connector",
			mutate: func(request *mcp.MCPElicitationRequest) {
				request.Meta.(map[string]any)["connector_id"] = "connector_other"
			},
		},
		{
			name: "different tool",
			mutate: func(request *mcp.MCPElicitationRequest) {
				request.Meta.(map[string]any)["tool_name"] = "inspect"
			},
		},
		{
			name: "different approval kind",
			mutate: func(request *mcp.MCPElicitationRequest) {
				request.Meta.(map[string]any)["codex_approval_kind"] = "tool_suggestion"
			},
		},
		{
			name: "non-empty form",
			mutate: func(request *mcp.MCPElicitationRequest) {
				request.RequestedSchema = map[string]any{
					"type":       "object",
					"properties": map[string]any{"confirmed": map[string]any{"type": "boolean"}},
				}
			},
		},
		{
			name: "non-node-repl server",
			mutate: func(request *mcp.MCPElicitationRequest) {
				request.ServerName = "codex_apps"
			},
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			reviewerCalled := false
			reviewer := guardianReviewerFunc(func(context.Context, string, string, string, state.Action) (state.ReviewDecision, string, error) {
				reviewerCalled = true
				return state.DecisionApproved, "", nil
			})
			handler := userCUAHandler(userCUAAuthority("user"), reviewer)
			request := userNodeReplElicitation("node_repl", userNodeReplElicitationMeta())
			testCase.mutate(request)

			response, err := handler.HandleMCPElicitation(context.Background(), request)
			if err != nil {
				t.Fatalf("HandleMCPElicitation() error = %v", err)
			}
			if response == nil || response.Action == mcp.MCPElicitationActionAccept {
				t.Fatalf("response = %#v, want the request to remain with the user", response)
			}
			if reviewerCalled {
				t.Fatal("User approval mode must not send the non-ordinary request to the reviewer")
			}
		})
	}
}

// TestUserApprovalNodeReplAutoApprovalHonorsCancellationLikeRust mirrors the
// upstream cancellation check: a cancelled turn cancels rather than accepts.
func TestUserApprovalNodeReplAutoApprovalHonorsCancellationLikeRust(t *testing.T) {
	handler := userCUAHandler(userCUAAuthority("user"), nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	response, err := handler.HandleMCPElicitation(ctx, userNodeReplElicitation("node_repl", userNodeReplElicitationMeta()))
	if err != nil {
		t.Fatalf("HandleMCPElicitation() error = %v", err)
	}
	if response == nil || response.Action != mcp.MCPElicitationActionCancel {
		t.Fatalf("response = %#v, want cancel", response)
	}
	meta, _ := response.Meta.(map[string]any)
	if meta["approvals_reviewer"] != "auto_review" {
		t.Fatalf("meta = %#v, want approvals_reviewer auto_review", response.Meta)
	}
}

// TestAutoReviewStillReviewsNodeReplExecutionLikeRust guards the new User-mode
// fast path from changing automatic review.
func TestAutoReviewStillReviewsNodeReplExecutionLikeRust(t *testing.T) {
	reviewed := false
	reviewer := guardianReviewerFunc(func(_ context.Context, threadID, turnID, targetItemID string, action state.Action) (state.ReviewDecision, string, error) {
		reviewed = true
		if threadID != "thread-user-cua" || turnID != "turn-user-cua" || targetItemID != "call-user-cua" {
			t.Fatalf("review context = %s/%s/%s", threadID, turnID, targetItemID)
		}
		if action.Server != "node_repl" || action.ToolName != "js" || action.ConnectorID != "node_repl" {
			t.Fatalf("reviewed action = %#v", action)
		}
		return state.DecisionApproved, "reviewed", nil
	})
	handler := userCUAHandler(userCUAAuthority("auto_review"), reviewer)

	response, err := handler.HandleMCPElicitation(context.Background(), userNodeReplElicitation("node_repl", userNodeReplElicitationMeta()))
	if err != nil {
		t.Fatalf("HandleMCPElicitation() error = %v", err)
	}
	if !reviewed {
		t.Fatal("automatic review did not run for node_repl execution")
	}
	if response == nil || response.Action != mcp.MCPElicitationActionAccept {
		t.Fatalf("response = %#v, want accept", response)
	}
}
