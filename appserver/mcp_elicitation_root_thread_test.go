package appserver

import (
	"context"
	"strings"
	"testing"
	"time"

	"codex_go/mcp"
	"codex_go/sandbox"
	"codex_go/session"
	"codex_go/state"
)

// TestRuntimeMCPElicitationAuthorityTracksTheOwningThreadLikeRust mirrors Rust
// #46066's "carry user-interaction eligibility through MCP runtime creation":
// the authority published for a delegated subagent's runtime disallows user
// interaction, while a root thread's allows it.
func TestRuntimeMCPElicitationAuthorityTracksTheOwningThreadLikeRust(t *testing.T) {
	store := session.NewStore(t.TempDir())
	now := time.Now().UTC()
	subagent := &session.Record{ID: "subagent-thread", CreatedAt: now, UpdatedAt: now, RecencyAt: now, Metadata: session.Metadata{
		CWD:          t.TempDir(),
		ThreadSource: "subAgentThreadSpawn",
		Originator:   "subagent",
	}}
	root := &session.Record{ID: "root-thread", CreatedAt: now, UpdatedAt: now, RecencyAt: now, Metadata: session.Metadata{CWD: t.TempDir()}}
	if err := store.Create(subagent); err != nil {
		t.Fatal(err)
	}
	if err := store.Create(root); err != nil {
		t.Fatal(err)
	}
	router := NewRuntimeRouter(RuntimeServices{ThreadRouter: NewRouter(store)})
	t.Cleanup(func() { _ = router.Close() })

	if authority := router.currentMCPElicitationAuthority("subagent-thread", "docs", ""); authority.AllowUserInteraction {
		t.Fatalf("subagent authority = %#v, want user interaction disabled", authority)
	}
	if authority := router.currentMCPElicitationAuthority("root-thread", "docs", ""); !authority.AllowUserInteraction {
		t.Fatalf("root authority = %#v, want user interaction enabled", authority)
	}
}

// subagentElicitationHandler returns a handler whose authority mirrors a
// delegated subagent's MCP runtime: Rust #46066 publishes
// allow_user_interaction = false for a non-root agent.
func subagentElicitationHandler(t *testing.T, profile *sandbox.PermissionProfile, approvalPolicy sandbox.AskForApproval, reviewer GuardianReviewer) *appserverMCPElicitationHandler {
	t.Helper()
	return &appserverMCPElicitationHandler{
		reviewer: reviewer,
		authority: func(string, string, string) mcpElicitationAuthority {
			return mcpElicitationAuthority{
				ApprovalPolicy:       approvalPolicy,
				ApprovalsReviewer:    "auto_review",
				PermissionProfile:    profile,
				AllowUserInteraction: false,
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

// TestAppserverMCPElicitationRejectsSubagentUserInputLikeRust mirrors Rust
// #46066: a non-root agent may not present an interactive MCP elicitation, so a
// request that needs user input is rejected with the handoff guidance before any
// automatic approval or review runs and before any prompt is registered.
func TestAppserverMCPElicitationRejectsSubagentUserInputLikeRust(t *testing.T) {
	broker := NewServerRequestBroker()
	broker.SetSink(ServerRequestSinkFunc(func(request *ServerRequest) {
		t.Fatalf("a subagent elicitation must not reach the client: %#v", request)
	}))
	reviewer := &fakeGuardianReviewer{decision: state.DecisionApproved}
	handler := subagentElicitationHandler(t, nil, sandbox.ApprovalOnRequest, reviewer)
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
			ServerName:      "docs",
			Method:          "elicitation/create",
			RequestedSchema: emptyMCPElicitationSchema(),
			Meta:            map[string]any{"codex_requires_user_input": true},
		},
		{
			// The explicit marker wins over the automatic review decision.
			ServerName:      "docs",
			Method:          "elicitation/create",
			RequestedSchema: emptyMCPElicitationSchema(),
			Meta: map[string]any{
				"codex_request_type":        "approval_request",
				"codex_approval_kind":       "mcp_tool_call",
				"codex_strict_auto_review":  true,
				"codex_requires_user_input": true,
				"tool_name":                 "access_browser_origin",
			},
		},
		{
			// A URL elicitation is the non-form MCP variant.
			ServerName:    "codex_apps",
			Method:        "elicitation/create",
			URL:           "https://example.com/auth",
			ElicitationID: "codex_apps_auth_call-1",
		},
		{
			ServerName: "docs",
			Method:     "openai/userVerification",
			Title:      "Approve purchase",
			Challenge:  "AQID",
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
		if err == nil || !strings.Contains(err.Error(), mcp.MCPElicitationHandoffMessage) {
			t.Fatalf("request %d error = %v, want the handoff guidance", index, err)
		}
		if response != nil {
			t.Fatalf("request %d response = %#v, want nil", index, response)
		}
	}
	if len(reviewer.actions) != 0 {
		t.Fatalf("guardian reviews = %#v, want none", reviewer.actions)
	}
}

// TestAppserverMCPElicitationKeepsSubagentAutomaticDecisionsLikeRust mirrors
// Rust #46066's subagent_permission_preserves_automatic_review_decisions: an
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
