package appserver

import (
	"context"
	"os"
	"testing"

	"codex_go/apps"
	"codex_go/config"
	"codex_go/mcp"
	"codex_go/state"
	"codex_go/tool"
	"codex_go/turn"
)

// Rust #44569: an optional Guardian review that cannot fit the complete action
// must not deny an MCP tool call; the approval falls back to the user prompt.
func TestMCPToolApprovalAskUserFallsBackToUserLikeRust(t *testing.T) {
	home := t.TempDir()
	cwd := t.TempDir()
	if err := os.WriteFile(config.ConfigPath(home), []byte("model = \"gpt-5.4\"\napprovals_reviewer = \"auto_review\"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile config error = %v", err)
	}
	reviewer := &fakeGuardianReviewer{decision: state.DecisionAskUser, reason: guardianInputBudgetMessage}
	router := NewRuntimeRouter(RuntimeServices{
		DefaultCWD:       cwd,
		Config:           config.NewConfigService(home),
		GuardianReviewer: reviewer,
	})
	t.Cleanup(func() { _ = router.Close() })
	params := &turn.TurnStartParams{ThreadID: "thread-1", CWD: cwd, Model: "gpt-5.4"}
	if err := router.threads.RegisterTurn("thread-1", "turn-1", nil, 0, params); err != nil {
		t.Fatalf("RegisterTurn() error = %v", err)
	}

	prompted := false
	handler := &appserverMCPToolApprovalHandler{
		router:   router,
		threadID: "thread-1",
		turnID:   "turn-1",
		responder: func(_ context.Context, _ string, args *tool.RequestUserInputArgs) (*tool.UserInputResponse, error) {
			prompted = true
			if len(args.Questions) != 1 {
				t.Fatalf("questions = %#v", args.Questions)
			}
			question := args.Questions[0]
			if question.ID != mcp.MCPToolApprovalQuestionIDPrefix+"_call-1" {
				t.Fatalf("question id = %q", question.ID)
			}
			return &tool.UserInputResponse{Answers: map[string]string{question.ID: mcp.MCPToolApprovalAccept}}, nil
		},
	}
	outcome, err := handler.ApproveMCPToolCall(context.Background(), &mcp.MCPToolApprovalRequest{
		Server:       "docs",
		Tool:         "search",
		Arguments:    map[string]any{"query": "release notes"},
		ApprovalMode: apps.AppToolApprovalAuto,
		ThreadID:     "thread-1",
		TurnID:       "turn-1",
		CallID:       "call-1",
	})
	if err != nil {
		t.Fatalf("ApproveMCPToolCall() error = %v", err)
	}
	if outcome.Decision != mcp.MCPToolApprovalApprove {
		t.Fatalf("decision = %v, want the user's approval", outcome.Decision)
	}
	if !prompted {
		t.Fatal("a budget-deferred MCP review did not reach the user approval prompt")
	}
	if len(reviewer.actions) != 1 {
		t.Fatalf("guardian actions = %#v", reviewer.actions)
	}
}

func mcpElicitationAskUserRequest(strict bool) *mcp.MCPElicitationRequest {
	meta := map[string]any{
		"codex_request_type":  "approval_request",
		"codex_approval_kind": "mcp_tool_call",
		"tool_name":           "search",
		"tool_params":         map[string]any{"query": "release notes"},
	}
	if strict {
		meta["codex_strict_auto_review"] = true
	}
	return &mcp.MCPElicitationRequest{
		ServerName:      "docs",
		ThreadID:        "thread-1",
		TurnID:          "turn-1",
		Method:          "elicitation/create",
		ElicitationID:   "elicitation-1",
		RequestedSchema: map[string]any{"type": "object", "properties": map[string]any{}},
		Meta:            meta,
	}
}

// Rust #44569: an optional MCP elicitation review that cannot fit the complete
// action falls back to the user, while a strict auto review still declines.
func TestMCPElicitationAskUserFallsBackToUserLikeRust(t *testing.T) {
	t.Run("optional review falls back", func(t *testing.T) {
		reviewer := &fakeGuardianReviewer{decision: state.DecisionAskUser, reason: guardianInputBudgetMessage}
		broker := NewServerRequestBroker()
		prompted := false
		broker.SetSink(ServerRequestSinkFunc(func(request *ServerRequest) {
			if request.Method != ServerRequestMCPElicitation {
				t.Fatalf("server request = %s", request.Method)
			}
			params, ok := request.Params.(*MCPElicitationRequestParams)
			if !ok || params.ServerName != "docs" || params.ThreadID != "thread-1" {
				t.Fatalf("elicitation params = %#v", request.Params)
			}
			prompted = true
			_, _ = broker.Resolve(OK(request.ID, &MCPElicitationRequestResponse{Action: MCPElicitationActionAccept}))
		}))
		response, err := (&appserverMCPElicitationHandler{broker: broker, reviewer: reviewer}).
			HandleMCPElicitation(context.Background(), mcpElicitationAskUserRequest(false))
		if err != nil {
			t.Fatalf("HandleMCPElicitation() error = %v", err)
		}
		if response == nil || response.Action != mcp.MCPElicitationActionAccept {
			t.Fatalf("response = %#v, want the user's accept", response)
		}
		if !prompted {
			t.Fatal("a budget-deferred MCP elicitation did not reach the user")
		}
		if len(reviewer.actions) != 1 {
			t.Fatalf("guardian actions = %#v", reviewer.actions)
		}
	})

	t.Run("strict review declines", func(t *testing.T) {
		reviewer := &fakeGuardianReviewer{decision: state.DecisionAskUser, reason: guardianInputBudgetMessage}
		response, err := (&appserverMCPElicitationHandler{reviewer: reviewer}).
			HandleMCPElicitation(context.Background(), mcpElicitationAskUserRequest(true))
		if err != nil {
			t.Fatalf("HandleMCPElicitation() error = %v", err)
		}
		if response == nil || response.Action != mcp.MCPElicitationActionDecline {
			t.Fatalf("response = %#v, want a strict decline", response)
		}
		meta, _ := response.Meta.(map[string]any)
		if meta["reason"] != guardianInputBudgetMessage {
			t.Fatalf("response meta = %#v", response.Meta)
		}
		if len(reviewer.actions) != 1 {
			t.Fatalf("guardian actions = %#v", reviewer.actions)
		}
	})
}
