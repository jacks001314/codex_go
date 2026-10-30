package appserver

import (
	"context"
	"testing"

	"codex_go/mcp"
	"codex_go/state"
)

func nodeReplNestedReviewRequest(serverName string, meta map[string]any) *mcp.MCPElicitationRequest {
	return &mcp.MCPElicitationRequest{
		ServerName:      serverName,
		ThreadID:        "thread-node-repl",
		TurnID:          "turn-node-repl",
		ElicitationID:   "nested-elicitation-1",
		Method:          "elicitation/create",
		RequestedSchema: map[string]any{"type": "object", "properties": map[string]any{}},
		Meta:            meta,
	}
}

func nodeReplNestedReviewMeta() map[string]any {
	return map[string]any{
		"codex_request_type":  "approval_request",
		"codex_approval_kind": "mcp_tool_call",
		"tool_name":           "browser.click",
		"tool_params":         map[string]any{"selector": "#submit"},
	}
}

// Mirrors Rust #42082: a nested elicitation from a Node REPL-backed server is
// attributed to the enclosing `js` execution when the reported `callId` names a
// live, same-server invocation from the current turn.
func TestNodeReplNestedReviewUsesOriginatingCallLikeRust(t *testing.T) {
	for _, serverName := range []string{"node_repl", "cua_repl"} {
		t.Run(serverName, func(t *testing.T) {
			service := mcp.NewMCPService(nil)
			release := service.RegisterToolInvocation(serverName, "call-1", mcp.MCPInvocationMetadata{
				ServerName: serverName,
				ToolName:   "js",
				Arguments:  map[string]any{"code": "await page.click('#submit')"},
				ThreadID:   "thread-node-repl",
				TurnID:     "turn-node-repl",
			})
			defer release()
			meta := nodeReplNestedReviewMeta()
			meta["callId"] = "call-1"
			targetID := ""
			reviewed := state.Action{}
			reviewer := guardianReviewerFunc(func(_ context.Context, _, _, targetItemID string, action state.Action) (state.ReviewDecision, string, error) {
				targetID = targetItemID
				reviewed = action
				return state.DecisionApproved, "", nil
			})
			response, err := (&appserverMCPElicitationHandler{service: service, reviewer: reviewer}).
				HandleMCPElicitation(context.Background(), nodeReplNestedReviewRequest(serverName, meta))
			if err != nil {
				t.Fatalf("HandleMCPElicitation() error = %v", err)
			}
			if response == nil || response.Action != mcp.MCPElicitationActionAccept {
				t.Fatalf("response = %#v, want an accept", response)
			}
			if targetID != "call-1" {
				t.Fatalf("review target = %q, want the originating call id", targetID)
			}
			// Each nested action and connector is still reviewed independently.
			if reviewed.Server != serverName || reviewed.ToolName != "browser.click" {
				t.Fatalf("reviewed action = %#v, want the nested identity", reviewed)
			}
		})
	}
}

// Mirrors Rust #42082: a missing, malformed, unknown, other-server, or stale
// `callId` falls back to the elicitation-specific target rather than attributing
// the review to an unrelated invocation.
func TestNodeReplNestedReviewFallsBackToElicitationTargetLikeRust(t *testing.T) {
	cases := []struct {
		name     string
		callID   string
		register bool
		foreign  bool
		release  bool
	}{
		{name: "missing call id"},
		{name: "unknown call id", callID: "unknown-call"},
		{name: "malformed call id", callID: "  "},
		{name: "other server call id", callID: "call-1", register: true, foreign: true},
		{name: "released call id", callID: "call-1", register: true, release: true},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			service := mcp.NewMCPService(nil)
			if testCase.register {
				serverName := "node_repl"
				if testCase.foreign {
					serverName = "codex_apps"
				}
				release := service.RegisterToolInvocation(serverName, "call-1", mcp.MCPInvocationMetadata{
					ServerName: serverName,
					ToolName:   "js",
				})
				if testCase.release {
					release()
				} else {
					defer release()
				}
			}
			meta := nodeReplNestedReviewMeta()
			if testCase.callID != "" {
				meta["callId"] = testCase.callID
			}
			targetID := ""
			reviewer := guardianReviewerFunc(func(_ context.Context, _, _, targetItemID string, _ state.Action) (state.ReviewDecision, string, error) {
				targetID = targetItemID
				return state.DecisionApproved, "", nil
			})
			if _, err := (&appserverMCPElicitationHandler{service: service, reviewer: reviewer}).
				HandleMCPElicitation(context.Background(), nodeReplNestedReviewRequest("node_repl", meta)); err != nil {
				t.Fatalf("HandleMCPElicitation() error = %v", err)
			}
			if targetID != "nested-elicitation-1" {
				t.Fatalf("review target = %q, want the elicitation-specific id", targetID)
			}
		})
	}
}
