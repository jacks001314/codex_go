package appserver

import (
	"context"
	"testing"

	"codex_go/mcp"
	"codex_go/sandbox"
	"codex_go/state"
)

// TestAppserverMCPElicitationAllowsSubagentUserInputLikeRust mirrors Rust
// #46877: explicit MCP elicitations can be surfaced by root and non-root
// agents, so a delegated subagent's form, browser-auth and URL requests reach
// the client and wait for its answer instead of failing with handoff guidance.
func TestAppserverMCPElicitationAllowsSubagentUserInputLikeRust(t *testing.T) {
	broker := NewServerRequestBroker()
	answered := 0
	broker.SetSink(ServerRequestSinkFunc(func(request *ServerRequest) {
		if request.Method != ServerRequestMCPElicitation {
			t.Fatalf("method = %s", request.Method)
		}
		answered++
		_, _ = broker.Resolve(OK(request.ID, &MCPElicitationRequestResponse{Action: MCPElicitationActionAccept}))
	}))
	handler := subagentElicitationHandler(t, nil, sandbox.ApprovalOnRequest, nil)
	handler.broker = broker
	requests := []*mcp.MCPElicitationRequest{
		{
			ServerName:      "docs",
			Method:          "elicitation/create",
			RequestedSchema: answeringMCPElicitationSchema(),
		},
		{
			// Browser sign-in can use an empty schema.
			ServerName:      "docs",
			Method:          "elicitation/create",
			RequestedSchema: emptyMCPElicitationSchema(),
			Meta:            map[string]any{"codex_approval_kind": "browser_auth"},
		},
		{
			// A URL elicitation is the non-form MCP variant.
			ServerName:    "codex_apps",
			Method:        "elicitation/create",
			URL:           "https://example.com/auth",
			ElicitationID: "codex_apps_auth_call-1",
		},
		{
			ServerName:      "docs",
			Method:          "openai/elicitation/create",
			Mode:            "form",
			RequestedSchema: answeringMCPElicitationSchema(),
		},
	}
	for index, request := range requests {
		response, err := handler.HandleMCPElicitation(context.Background(), request)
		if err != nil {
			t.Fatalf("request %d error = %v, want the client prompt", index, err)
		}
		if response == nil || response.Action != mcp.MCPElicitationActionAccept {
			t.Fatalf("request %d response = %#v, want the client acceptance", index, response)
		}
	}
	if answered != len(requests) {
		t.Fatalf("client prompts = %d, want %d", answered, len(requests))
	}
}

// subagentElicitationHandler returns a handler whose authority mirrors a
// delegated subagent's MCP runtime. Rust #46877 removed the root-only guard, so
// the authority itself no longer carries user-interaction eligibility.
func subagentElicitationHandler(t *testing.T, profile *sandbox.PermissionProfile, approvalPolicy sandbox.AskForApproval, reviewer GuardianReviewer) *appserverMCPElicitationHandler {
	t.Helper()
	return &appserverMCPElicitationHandler{
		reviewer: reviewer,
		authority: func(string, string, string) mcpElicitationAuthority {
			return mcpElicitationAuthority{
				ApprovalPolicy:    approvalPolicy,
				ApprovalsReviewer: "auto_review",
				PermissionProfile: profile,
			}
		},
	}
}

func emptyMCPElicitationSchema() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{}}
}

func answeringMCPElicitationSchema() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{"answer": map[string]any{"type": "string"}}}
}

// TestAppserverMCPElicitationKeepsSubagentAutomaticDecisionsLikeRust mirrors
// Rust #46877's preserved automatic-review behaviour: an
// elicitation that needs no user input still gets the automatic approval or
// review decision instead of the handoff rejection.
func TestAppserverMCPElicitationKeepsSubagentAutomaticDecisionsLikeRust(t *testing.T) {
	reviewer := &fakeGuardianReviewer{decision: state.DecisionApproved}
	handler := subagentElicitationHandler(t, nil, sandbox.ApprovalOnRequest, reviewer)
	response, err := handler.HandleMCPElicitation(context.Background(), &mcp.MCPElicitationRequest{
		ServerName:      "codex_apps",
		Method:          "elicitation/create",
		RequestedSchema: emptyMCPElicitationSchema(),
		Meta: map[string]any{
			"codex_request_type":  "approval_request",
			"codex_approval_kind": "mcp_tool_call",
			"tool_name":           "access_browser_origin",
			"tool_params":         map[string]any{"origin": "https://example.com"},
		},
	})
	if err != nil {
		t.Fatalf("HandleMCPElicitation() error = %v", err)
	}
	if response == nil || response.Action != mcp.MCPElicitationActionAccept {
		t.Fatalf("response = %#v, want the automatic approval", response)
	}
	if len(reviewer.actions) != 1 {
		t.Fatalf("guardian reviews = %#v, want one review", reviewer.actions)
	}

	// A full-access subagent still auto-accepts an empty form.
	profile := sandbox.FullAccessPermissionProfile()
	handler = subagentElicitationHandler(t, &profile, sandbox.ApprovalNever, nil)
	response, err = handler.HandleMCPElicitation(context.Background(), &mcp.MCPElicitationRequest{
		ServerName:      "docs",
		Method:          "elicitation/create",
		RequestedSchema: emptyMCPElicitationSchema(),
	})
	if err != nil {
		t.Fatalf("HandleMCPElicitation() error = %v", err)
	}
	if response == nil || response.Action != mcp.MCPElicitationActionAccept {
		t.Fatalf("full-access response = %#v, want auto accept", response)
	}
}
