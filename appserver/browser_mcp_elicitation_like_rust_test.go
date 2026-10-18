package appserver

import (
	"context"
	"testing"

	"codex_go/mcp"
	"codex_go/state"
)

func browserNestedReviewRequest(meta map[string]any) *mcp.MCPElicitationRequest {
	return &mcp.MCPElicitationRequest{
		ServerName:      mcp.RuntimeCodexAppsMCPServerName,
		ThreadID:        "thread-1",
		TurnID:          "turn-1",
		ElicitationID:   "nested-elicitation-1",
		Method:          "elicitation/create",
		RequestedSchema: map[string]any{"type": "object", "properties": map[string]any{}},
		Meta:            meta,
	}
}

func browserNestedReviewMeta(strict bool, toolName string) map[string]any {
	meta := map[string]any{
		"codex_request_type":  "approval_request",
		"codex_approval_kind": "mcp_tool_call",
		"connector_id":        mcp.BrowserConnectorID,
		"tool_name":           toolName,
		"tool_params":         map[string]any{"record": map[string]any{"value": float64(42)}},
	}
	if strict {
		meta["codex_strict_auto_review"] = true
	}
	return meta
}

func registerBrowserInvocation(service *mcp.MCPService, callID string, connectorID string, toolName string) func() {
	return service.RegisterToolInvocation("codex_apps", callID, mcp.MCPInvocationMetadata{
		ServerName:    "codex_apps",
		ToolName:      toolName,
		ConnectorID:   connectorID,
		ConnectorName: "Browser",
		Arguments:     map[string]any{"code": "write_record()"},
	})
}

// Rust #47647: strict nested review must fail closed, without reaching the
// Guardian reviewer or the user, when the originating invocation is missing,
// unknown, or registered to a different connector.
func TestStrictBrowserNestedReviewRequiresLiveBrowserIdentityLikeRust(t *testing.T) {
	tests := []struct {
		name        string
		callID      string
		connectorID string
		register    bool
	}{
		{name: "missing correlation"},
		{name: "unknown correlation", callID: "unknown-invocation", register: false},
		{name: "non-browser registration", callID: "call-1", connectorID: "connector_other", register: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service := mcp.NewMCPService(nil)
			if test.register {
				release := registerBrowserInvocation(service, "call-1", test.connectorID, "_js")
				defer release()
			}
			meta := browserNestedReviewMeta(true, "webmcp:example.com:write_record")
			if test.callID != "" {
				meta["_codex_apps"] = map[string]any{"call_id": test.callID}
			}
			reviewerCalled := false
			reviewer := guardianReviewerFunc(func(context.Context, string, string, string, state.Action) (state.ReviewDecision, string, error) {
				reviewerCalled = true
				return state.DecisionApproved, "", nil
			})
			response, err := (&appserverMCPElicitationHandler{service: service, reviewer: reviewer}).
				HandleMCPElicitation(context.Background(), browserNestedReviewRequest(meta))
			if err != nil {
				t.Fatalf("HandleMCPElicitation() error = %v", err)
			}
			if response == nil || response.Action != mcp.MCPElicitationActionDecline {
				t.Fatalf("response = %#v, want a decline", response)
			}
			responseMeta, _ := response.Meta.(map[string]any)
			if responseMeta["message"] != mcpStrictAutoReviewDeclineMessage {
				t.Fatalf("response meta = %#v", response.Meta)
			}
			if reviewerCalled {
				t.Fatal("an unverified strict request reached the Guardian reviewer")
			}
		})
	}
}

// Rust #47647: a strict Browser elicitation correlated with a live Browser
// invocation keeps its own action identity but is attributed to the
// originating call.
func TestStrictBrowserNestedReviewUsesLiveInvocationCorrelationLikeRust(t *testing.T) {
	service := mcp.NewMCPService(nil)
	release := registerBrowserInvocation(service, "call-1", mcp.BrowserConnectorID, "_js")
	defer release()
	meta := browserNestedReviewMeta(true, "webmcp:example.com:write_record")
	meta["_codex_apps"] = map[string]any{"call_id": "call-1"}
	reviewed := state.Action{}
	targetID := ""
	reviewer := guardianReviewerFunc(func(_ context.Context, threadID, turnID, targetItemID string, action state.Action) (state.ReviewDecision, string, error) {
		if threadID != "thread-1" || turnID != "turn-1" {
			t.Fatalf("review context = %s/%s", threadID, turnID)
		}
		targetID = targetItemID
		reviewed = action
		return state.DecisionApproved, "", nil
	})
	response, err := (&appserverMCPElicitationHandler{service: service, reviewer: reviewer}).
		HandleMCPElicitation(context.Background(), browserNestedReviewRequest(meta))
	if err != nil {
		t.Fatalf("HandleMCPElicitation() error = %v", err)
	}
	if response == nil || response.Action != mcp.MCPElicitationActionAccept {
		t.Fatalf("response = %#v, want an accept", response)
	}
	if targetID != "call-1" {
		t.Fatalf("review target = %q, want the originating call id", targetID)
	}
	if reviewed.ToolName != "webmcp:example.com:write_record" || reviewed.ConnectorID != mcp.BrowserConnectorID ||
		reviewed.Server != "codex_apps" {
		t.Fatalf("reviewed action = %#v, want the nested action identity", reviewed)
	}
}

// Rust #47647: ordinary nested reviews also inherit the originating call id
// while keeping the nested action's own identity.
func TestBrowserNestedReviewInheritsOriginatingCallIDLikeRust(t *testing.T) {
	service := mcp.NewMCPService(nil)
	release := registerBrowserInvocation(service, "call-1", mcp.BrowserConnectorID, "_js")
	defer release()
	meta := browserNestedReviewMeta(false, "access_browser_origin")
	meta["_codex_apps"] = map[string]any{"call_id": "call-1"}
	targetID := ""
	reviewer := guardianReviewerFunc(func(_ context.Context, _, _, targetItemID string, _ state.Action) (state.ReviewDecision, string, error) {
		targetID = targetItemID
		return state.DecisionApproved, "", nil
	})
	response, err := (&appserverMCPElicitationHandler{service: service, reviewer: reviewer}).
		HandleMCPElicitation(context.Background(), browserNestedReviewRequest(meta))
	if err != nil {
		t.Fatalf("HandleMCPElicitation() error = %v", err)
	}
	if response == nil || response.Action != mcp.MCPElicitationActionAccept {
		t.Fatalf("response = %#v, want an accept", response)
	}
	if targetID != "call-1" {
		t.Fatalf("review target = %q, want the originating call id", targetID)
	}
}

// Rust #47647: once the invocation completes, strict review can no longer
// verify the correlation and fails closed.
func TestStrictBrowserNestedReviewAfterReleaseDeclinesLikeRust(t *testing.T) {
	service := mcp.NewMCPService(nil)
	release := registerBrowserInvocation(service, "call-1", mcp.BrowserConnectorID, "_js")
	release()
	meta := browserNestedReviewMeta(true, "webmcp:example.com:write_record")
	meta["_codex_apps"] = map[string]any{"call_id": "call-1"}
	reviewerCalled := false
	reviewer := guardianReviewerFunc(func(context.Context, string, string, string, state.Action) (state.ReviewDecision, string, error) {
		reviewerCalled = true
		return state.DecisionApproved, "", nil
	})
	response, err := (&appserverMCPElicitationHandler{service: service, reviewer: reviewer}).
		HandleMCPElicitation(context.Background(), browserNestedReviewRequest(meta))
	if err != nil {
		t.Fatalf("HandleMCPElicitation() error = %v", err)
	}
	if response == nil || response.Action != mcp.MCPElicitationActionDecline {
		t.Fatalf("response = %#v, want a decline", response)
	}
	if reviewerCalled {
		t.Fatal("a completed invocation still reached the Guardian reviewer")
	}
}

// Rust #47647: a hosted connector other than the Browser, when strict review
// can correlate the live invocation, keeps the registered outer identity.
func TestStrictHostedConnectorUsesRegisteredOuterInvocationLikeRust(t *testing.T) {
	service := mcp.NewMCPService(nil)
	release := service.RegisterToolInvocation("codex_apps", "call-1", mcp.MCPInvocationMetadata{
		ServerName:    "codex_apps",
		ToolName:      "calendar_create",
		ConnectorID:   "connector_calendar",
		ConnectorName: "Calendar",
		ToolTitle:     "Create event",
		Arguments:     map[string]any{"title": "Lunch"},
	})
	defer release()
	meta := browserNestedReviewMeta(true, "calendar_create")
	meta["connector_id"] = "connector_calendar"
	meta["connector_name"] = "Spoofed"
	meta["tool_params"] = map[string]any{"title": "Spoofed"}
	meta["_codex_apps"] = map[string]any{"call_id": "call-1"}
	reviewed := state.Action{}
	targetID := ""
	reviewer := guardianReviewerFunc(func(_ context.Context, _, _, targetItemID string, action state.Action) (state.ReviewDecision, string, error) {
		targetID = targetItemID
		reviewed = action
		return state.DecisionApproved, "", nil
	})
	response, err := (&appserverMCPElicitationHandler{service: service, reviewer: reviewer}).
		HandleMCPElicitation(context.Background(), browserNestedReviewRequest(meta))
	if err != nil {
		t.Fatalf("HandleMCPElicitation() error = %v", err)
	}
	if response == nil || response.Action != mcp.MCPElicitationActionAccept {
		t.Fatalf("response = %#v, want an accept", response)
	}
	if targetID != "call-1" {
		t.Fatalf("review target = %q, want the originating call id", targetID)
	}
	arguments, _ := reviewed.Extra["arguments"].(map[string]any)
	if reviewed.ConnectorID != "connector_calendar" || reviewed.ConnectorName != "Calendar" ||
		reviewed.ToolName != "calendar_create" || arguments["title"] != "Lunch" {
		t.Fatalf("reviewed action = %#v, want the registered outer invocation", reviewed)
	}
}
