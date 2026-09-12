package appserver

import (
	"context"
	"errors"
	"testing"

	"codex_go/apps"
	"codex_go/mcp"
	"codex_go/sandbox"
	"codex_go/tool"
)

// TestAppserverMCPToolApprovalUsesElicitationLikeRust covers the elicitation
// transport used when tool_call_mcp_elicitation is enabled: the request carries
// the approval schema and meta the TUI renders, and the answer maps back to the
// session-remember or persistent-approval decision.
func TestAppserverMCPToolApprovalUsesElicitationLikeRust(t *testing.T) {
	run := func(t *testing.T, action MCPElicitationAction, meta map[string]any) (mcp.MCPToolApprovalDecision, *MCPElicitationRequestParams, *appserverMCPToolApprovalHandler) {
		t.Helper()
		router := NewRuntimeRouter(RuntimeServices{})
		t.Cleanup(func() { _ = router.Close() })
		var params *MCPElicitationRequestParams
		router.SetServerRequestSink(ServerRequestSinkFunc(func(request *ServerRequest) {
			if request.Method != ServerRequestMCPElicitation {
				t.Errorf("unexpected server request %s", request.Method)
				return
			}
			params, _ = request.Params.(*MCPElicitationRequestParams)
			router.requireServerRequests().Resolve(OK(request.ID, &MCPElicitationRequestResponse{Action: action, Meta: meta}))
		}))
		handler := &appserverMCPToolApprovalHandler{
			router: router,
			responder: func(context.Context, *tool.RequestUserInputArgs) (*tool.UserInputResponse, error) {
				t.Error("the elicitation transport must not fall back to user input")
				return nil, nil
			},
			threadID:                  "thread-1",
			turnID:                    "turn-1",
			approvalPolicy:            sandbox.ApprovalOnRequest,
			persistentApprovalAllowed: true,
			elicitationEnabled:        true,
		}
		sessionKey := mcp.MCPToolApprovalKey{Server: "docs", Tool: "search"}
		decision, err := handler.ApproveMCPToolCall(context.Background(), &mcp.MCPToolApprovalRequest{
			Server:                  "docs",
			Tool:                    "search",
			ToolTitle:               "Search docs",
			ApprovalMode:            apps.AppToolApprovalAuto,
			CallID:                  "call-1",
			SessionKey:              &sessionKey,
			AllowSessionRemember:    true,
			AllowPersistentApproval: true,
		})
		if err != nil {
			t.Fatalf("ApproveMCPToolCall() error = %v", err)
		}
		return decision, params, handler
	}

	decision, params, handler := run(t, MCPElicitationActionAccept, map[string]any{"persist": "session"})
	if decision != mcp.MCPToolApprovalApproveForSession {
		t.Fatalf("decision = %v, want a session approval", decision)
	}
	if !handler.router.mcpToolApprovalRemembered("thread-1", mcp.MCPToolApprovalKey{Server: "docs", Tool: "search"}) {
		t.Fatal("session approval was not remembered")
	}
	if params == nil {
		t.Fatal("no elicitation request was sent")
	}
	if params.ElicitationID != "mcp_tool_call_approval_call-1" || params.Mode == "" {
		t.Fatalf("elicitation params = %#v", params)
	}
	meta, _ := params.Meta.(map[string]any)
	for key, want := range map[string]any{
		"response_mode":       "approval_action",
		"codex_request_type":  "approval_request",
		"codex_approval_kind": "mcp_tool_call",
		"tool_name":           "search",
		"tool_title":          "Search docs",
		"connector_id":        nil,
	} {
		if want == nil {
			continue
		}
		if meta[key] != want {
			t.Fatalf("elicitation meta[%s] = %#v, want %#v (meta=%#v)", key, meta[key], want, meta)
		}
	}
	schema, ok := params.RequestedSchema.(map[string]any)
	if !ok {
		t.Fatalf("requested schema = %#v", params.RequestedSchema)
	}
	properties, _ := schema["properties"].(map[string]any)
	field, _ := properties["decision"].(map[string]any)
	values, _ := field["enum"].([]any)
	wantValues := []string{"accept", "accept_session", "accept_always", "decline", "cancel"}
	if len(values) != len(wantValues) {
		t.Fatalf("approval options = %#v", values)
	}
	for index, want := range wantValues {
		if values[index] != want {
			t.Fatalf("option %d = %#v, want %q", index, values[index], want)
		}
	}

	declineDecision, _, declineHandler := run(t, MCPElicitationActionDecline, nil)
	if declineDecision != mcp.MCPToolApprovalReject {
		t.Fatalf("decline decision = %v, want a rejection", declineDecision)
	}
	if declineHandler.router.mcpToolApprovalRemembered("thread-1", mcp.MCPToolApprovalKey{Server: "docs", Tool: "search"}) {
		t.Fatal("a rejected call must not be remembered")
	}
}

func newMCPToolApprovalTestHandler(
	responder tool.UserInputResponder,
	persistent bool,
) *appserverMCPToolApprovalHandler {
	return &appserverMCPToolApprovalHandler{
		router:                    &RuntimeRouter{},
		responder:                 responder,
		threadID:                  "thread-1",
		turnID:                    "turn-1",
		approvalPolicy:            sandbox.ApprovalOnRequest,
		persistentApprovalAllowed: persistent,
	}
}

// TestAppserverMCPToolApprovalAnswersLikeRust covers the answer mapping, the
// session-remembered short-circuit, and the abort paths.
func TestAppserverMCPToolApprovalAnswersLikeRust(t *testing.T) {
	sessionKey := mcp.MCPToolApprovalKey{Server: "docs", Tool: "search"}
	request := func() *mcp.MCPToolApprovalRequest {
		return &mcp.MCPToolApprovalRequest{
			Server:                  "docs",
			Tool:                    "search",
			ApprovalMode:            apps.AppToolApprovalAuto,
			CallID:                  "call-1",
			SessionKey:              &sessionKey,
			AllowSessionRemember:    true,
			AllowPersistentApproval: true,
		}
	}

	t.Run("accept", func(t *testing.T) {
		handler := newMCPToolApprovalTestHandler(func(_ context.Context, args *tool.RequestUserInputArgs) (*tool.UserInputResponse, error) {
			question := args.Questions[0]
			if question.ID != mcp.MCPToolApprovalQuestionIDPrefix+"_call-1" || question.Header != mcp.MCPToolApprovalHeader {
				t.Fatalf("question = %#v", question)
			}
			wantLabels := []string{
				mcp.MCPToolApprovalAccept,
				mcp.MCPToolApprovalAcceptForSession,
				mcp.MCPToolApprovalAcceptAndRemember,
				mcp.MCPToolApprovalCancel,
			}
			if len(question.Options) != len(wantLabels) {
				t.Fatalf("options = %#v", question.Options)
			}
			return &tool.UserInputResponse{Answers: map[string]string{question.ID: mcp.MCPToolApprovalAccept}}, nil
		}, true)
		decision, err := handler.ApproveMCPToolCall(context.Background(), request())
		if err != nil || decision != mcp.MCPToolApprovalApprove {
			t.Fatalf("decision = %v, err = %v", decision, err)
		}
		if handler.router.mcpToolApprovalRemembered("thread-1", sessionKey) {
			t.Fatal("a one-shot approval must not be remembered")
		}
	})

	t.Run("accept for session is remembered", func(t *testing.T) {
		calls := 0
		handler := newMCPToolApprovalTestHandler(func(_ context.Context, args *tool.RequestUserInputArgs) (*tool.UserInputResponse, error) {
			calls++
			return &tool.UserInputResponse{Answers: map[string]string{args.Questions[0].ID: mcp.MCPToolApprovalAcceptForSession}}, nil
		}, true)
		decision, err := handler.ApproveMCPToolCall(context.Background(), request())
		if err != nil || decision != mcp.MCPToolApprovalApproveForSession {
			t.Fatalf("decision = %v, err = %v", decision, err)
		}
		if !handler.router.mcpToolApprovalRemembered("thread-1", sessionKey) {
			t.Fatal("session approval was not remembered")
		}
		decision, err = handler.ApproveMCPToolCall(context.Background(), request())
		if err != nil || decision != mcp.MCPToolApprovalApprove {
			t.Fatalf("remembered decision = %v, err = %v", decision, err)
		}
		if calls != 1 {
			t.Fatalf("responder calls = %d, want 1", calls)
		}
	})

	t.Run("cancel aborts", func(t *testing.T) {
		handler := newMCPToolApprovalTestHandler(func(_ context.Context, args *tool.RequestUserInputArgs) (*tool.UserInputResponse, error) {
			return &tool.UserInputResponse{Answers: map[string]string{args.Questions[0].ID: mcp.MCPToolApprovalCancel}}, nil
		}, true)
		decision, err := handler.ApproveMCPToolCall(context.Background(), request())
		if err != nil || decision != mcp.MCPToolApprovalDeny {
			t.Fatalf("decision = %v, err = %v", decision, err)
		}
	})

	t.Run("responder failure aborts", func(t *testing.T) {
		handler := newMCPToolApprovalTestHandler(func(context.Context, *tool.RequestUserInputArgs) (*tool.UserInputResponse, error) {
			return nil, errors.New("client disconnected")
		}, true)
		decision, err := handler.ApproveMCPToolCall(context.Background(), request())
		if err != nil || decision != mcp.MCPToolApprovalDeny {
			t.Fatalf("decision = %v, err = %v", decision, err)
		}
	})

	t.Run("prompt mode downgrades remembered answers", func(t *testing.T) {
		handler := newMCPToolApprovalTestHandler(func(_ context.Context, args *tool.RequestUserInputArgs) (*tool.UserInputResponse, error) {
			return &tool.UserInputResponse{Answers: map[string]string{args.Questions[0].ID: mcp.MCPToolApprovalAcceptAndRemember}}, nil
		}, true)
		mode := apps.AppToolApprovalPrompt
		promptRequest := request()
		promptRequest.ApprovalMode = mode
		promptRequest.SessionKey = nil
		promptRequest.AllowSessionRemember = false
		promptRequest.AllowPersistentApproval = false
		decision, err := handler.ApproveMCPToolCall(context.Background(), promptRequest)
		if err != nil || decision != mcp.MCPToolApprovalApprove {
			t.Fatalf("decision = %v, err = %v", decision, err)
		}
	})
}

// TestAppserverMCPToolApprovalForgetsOnUnload covers the session store reset.
func TestAppserverMCPToolApprovalForgetsOnUnload(t *testing.T) {
	router := &RuntimeRouter{}
	key := mcp.MCPToolApprovalKey{Server: "docs", Tool: "search"}
	router.rememberMCPToolApproval("thread-1", key)
	if !router.mcpToolApprovalRemembered("thread-1", key) {
		t.Fatal("approval was not remembered")
	}
	router.forgetMCPToolApprovals("thread-1")
	if router.mcpToolApprovalRemembered("thread-1", key) {
		t.Fatal("approval survived unload")
	}
}

// TestAppserverMCPToolApprovalRequiresRequestSink pins that a runtime without a
// client request sink does not enable prompting.
func TestAppserverMCPToolApprovalRequiresRequestSink(t *testing.T) {
	router := &RuntimeRouter{}
	if options := router.newAppserverMCPToolApprovalOptions(nil, mcp.NewMCPService(nil), "thread-1", "turn-1", sandbox.ApprovalOnRequest); options != nil {
		t.Fatalf("options = %#v, want nil without a request sink", options)
	}
}
