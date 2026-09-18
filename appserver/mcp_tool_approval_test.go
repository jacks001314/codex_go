package appserver

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codex_go/apps"
	"codex_go/codexapi"
	"codex_go/config"
	"codex_go/mcp"
	"codex_go/sandbox"
	"codex_go/state"
	"codex_go/tool"
	"codex_go/turn"
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
			allowUserInteraction:      true,
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
		return decision.Decision, params, handler
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

// TestAppserverMCPToolApprovalHandsSubagentRequestsToTheParentLikeRust mirrors
// Rust #46066's request_mcp_tool_user_approval: a delegated subagent never
// prompts the user, so the call is denied with the handoff guidance that tells
// the agent to ask its parent and to wait before retrying.
func TestAppserverMCPToolApprovalHandsSubagentRequestsToTheParentLikeRust(t *testing.T) {
	router := NewRuntimeRouter(RuntimeServices{})
	t.Cleanup(func() { _ = router.Close() })
	prompted := false
	router.SetServerRequestSink(ServerRequestSinkFunc(func(*ServerRequest) { prompted = true }))
	handler := &appserverMCPToolApprovalHandler{
		router: router,
		responder: func(context.Context, *tool.RequestUserInputArgs) (*tool.UserInputResponse, error) {
			prompted = true
			return nil, nil
		},
		threadID:             "thread-1",
		turnID:               "turn-1",
		approvalPolicy:       sandbox.ApprovalOnRequest,
		elicitationEnabled:   true,
		allowUserInteraction: false,
	}
	sessionKey := mcp.MCPToolApprovalKey{Server: "docs", Tool: "search"}
	outcome, err := handler.ApproveMCPToolCall(context.Background(), &mcp.MCPToolApprovalRequest{
		Server:               "docs",
		Tool:                 "search",
		ApprovalMode:         apps.AppToolApprovalAuto,
		CallID:               "call-1",
		SessionKey:           &sessionKey,
		AllowSessionRemember: true,
	})
	if err != nil {
		t.Fatalf("ApproveMCPToolCall() error = %v", err)
	}
	if outcome.Decision != mcp.MCPToolApprovalDeny {
		t.Fatalf("decision = %v, want deny", outcome.Decision)
	}
	if outcome.Message != mcp.MCPElicitationHandoffMessage {
		t.Fatalf("message = %q, want the handoff guidance", outcome.Message)
	}
	if prompted {
		t.Fatal("a subagent must not prompt the user")
	}
	if router.mcpToolApprovalRemembered("thread-1", sessionKey) {
		t.Fatal("a handed-off request must not be remembered")
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
		// The root thread may present interactive MCP prompts (Rust #46066).
		allowUserInteraction: true,
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
		outcome, err := handler.ApproveMCPToolCall(context.Background(), request())
		decision := outcome.Decision
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
		outcome, err := handler.ApproveMCPToolCall(context.Background(), request())
		decision := outcome.Decision
		if err != nil || decision != mcp.MCPToolApprovalApproveForSession {
			t.Fatalf("decision = %v, err = %v", decision, err)
		}
		if !handler.router.mcpToolApprovalRemembered("thread-1", sessionKey) {
			t.Fatal("session approval was not remembered")
		}
		outcome, err = handler.ApproveMCPToolCall(context.Background(), request())
		decision = outcome.Decision
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
		outcome, err := handler.ApproveMCPToolCall(context.Background(), request())
		decision := outcome.Decision
		if err != nil || decision != mcp.MCPToolApprovalDeny {
			t.Fatalf("decision = %v, err = %v", decision, err)
		}
	})

	t.Run("responder failure aborts", func(t *testing.T) {
		handler := newMCPToolApprovalTestHandler(func(context.Context, *tool.RequestUserInputArgs) (*tool.UserInputResponse, error) {
			return nil, errors.New("client disconnected")
		}, true)
		outcome, err := handler.ApproveMCPToolCall(context.Background(), request())
		decision := outcome.Decision
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
		outcome, err := handler.ApproveMCPToolCall(context.Background(), promptRequest)
		decision := outcome.Decision
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

// Rust build_mcp_tool_call_request_meta reports the turn's metadata document to
// MCP servers, without the Responses request identity, and flags a turn that
// asked the user for input.
func TestMCPTurnMetadataProviderLikeRust(t *testing.T) {
	router := NewRuntimeRouter(RuntimeServices{})
	if err := router.threads.RegisterTurn("thread-1", "turn-1", nil, 0, nil); err != nil {
		t.Fatalf("RegisterTurn() error = %v", err)
	}
	document := `{"session_id":"session","thread_id":"thread-1","turn_id":"turn-1",` +
		`"installation_id":"install","window_id":"window","request_kind":"turn",` +
		`"agent_name":"/root","parent_turn_id":"parent-turn","root_turn_id":"root-turn",` +
		`"model":"gpt-5.4","codex_version":"1.2.3"}`
	if !router.threads.UpdateTurn("thread-1", "turn-1", func(active *activeRuntimeTurn) {
		active.RunConfig = &appTurnRunConfig{ClientMetadata: map[string]string{
			codexapi.ClientCodexTurnMetadataHeader: document,
		}}
	}) {
		t.Fatal("UpdateTurn() did not find the registered turn")
	}

	provider := router.mcpTurnMetadataProvider("thread-1", "turn-1")
	meta := provider()
	if meta == nil {
		t.Fatal("the MCP turn metadata provider returned no document")
	}
	for _, key := range []string{"installation_id", "window_id", "request_kind", "agent_name", "parent_turn_id", "root_turn_id"} {
		if _, ok := meta[key]; ok {
			t.Fatalf("MCP turn metadata carries %q: %#v", key, meta)
		}
	}
	if meta["thread_id"] != "thread-1" || meta["turn_id"] != "turn-1" || meta["model"] != "gpt-5.4" {
		t.Fatalf("MCP turn metadata = %#v", meta)
	}
	if _, ok := meta["user_input_requested_during_turn"]; ok {
		t.Fatalf("MCP turn metadata flagged user input the turn never requested: %#v", meta)
	}

	router.markTurnUserInputRequested("thread-1", "turn-1")
	if meta := provider(); meta["user_input_requested_during_turn"] != true {
		t.Fatalf("MCP turn metadata missing the user-input flag: %#v", meta)
	}

	// A turn that has no metadata (or is gone) reports nothing.
	if meta := router.mcpTurnMetadataProvider("thread-1", "turn-2")(); meta != nil {
		t.Fatalf("unknown turn produced a document: %#v", meta)
	}
}

// Rust Session::request_approval: an MCP tool call runs PermissionRequest hooks
// first (hook tool name + arguments) and then the Guardian review when the turn
// auto-reviews, before any user prompt.
func TestMCPToolApprovalRunsHooksAndGuardianLikeRust(t *testing.T) {
	newRouter := func(t *testing.T, configExtra string, hookCommand string, reviewer GuardianReviewer) *RuntimeRouter {
		t.Helper()
		home := t.TempDir()
		cwd := t.TempDir()
		projectTrust := strings.ReplaceAll(filepath.Clean(cwd), `\`, `\\`)
		configBody := "model = \"gpt-5.4\"\nbypass_hook_trust = true\n" + configExtra +
			"[projects.\"" + projectTrust + "\"]\ntrust_level = \"trusted\"\n"
		if err := os.WriteFile(config.ConfigPath(home), []byte(configBody), 0o600); err != nil {
			t.Fatalf("WriteFile config error = %v", err)
		}
		if hookCommand != "" {
			hooksDir := filepath.Join(cwd, ".gcode")
			if err := os.MkdirAll(hooksDir, 0o700); err != nil {
				t.Fatalf("MkdirAll() error = %v", err)
			}
			hooksJSON, err := json.Marshal(map[string]any{
				"hooks": map[string]any{
					"PermissionRequest": []any{map[string]any{
						"matcher": "mcp_tool",
						"hooks":   []any{map[string]any{"type": "command", "command": hookCommand}},
					}},
				},
			})
			if err != nil {
				t.Fatalf("Marshal hooks error = %v", err)
			}
			if err := os.WriteFile(filepath.Join(hooksDir, "hooks.json"), hooksJSON, 0o600); err != nil {
				t.Fatalf("WriteFile hooks error = %v", err)
			}
		}
		router := NewRuntimeRouter(RuntimeServices{
			DefaultCWD:       cwd,
			Config:           config.NewConfigService(home),
			HooksDiscovery:   NewHookDiscoveryService(home),
			HookRunner:       NewHookRunner(),
			GuardianReviewer: reviewer,
		})
		params := &turn.TurnStartParams{ThreadID: "thread-1", CWD: cwd, Model: "gpt-5.4"}
		if err := router.threads.RegisterTurn("thread-1", "turn-1", nil, 0, params); err != nil {
			t.Fatalf("RegisterTurn() error = %v", err)
		}
		return router
	}
	approve := func(t *testing.T, router *RuntimeRouter) (mcp.MCPToolApprovalDecision, error) {
		t.Helper()
		handler := &appserverMCPToolApprovalHandler{router: router, threadID: "thread-1", turnID: "turn-1"}
		outcome, err := handler.ApproveMCPToolCall(context.Background(), &mcp.MCPToolApprovalRequest{
			Server:       "server",
			Tool:         "tool",
			Arguments:    map[string]any{"path": "src/main.go"},
			HookToolName: &tool.HookToolName{Name: "mcp_tool"},
			CallID:       "call-1",
		})
		return outcome.Decision, err
	}

	decision, err := approve(t, newRouter(t, "", hookRunnerOutputCommand(`{"hookSpecificOutput":{"hookEventName":"PermissionRequest","decision":{"behavior":"allow"}}}`, ""), nil))
	if err != nil || decision != mcp.MCPToolApprovalApprove {
		t.Fatalf("hook allow = %v err=%v", decision, err)
	}
	decision, err = approve(t, newRouter(t, "", hookRunnerPermissionRequestDenyCommand("no tools for you"), nil))
	if decision != mcp.MCPToolApprovalDeny || err == nil || !strings.Contains(err.Error(), "no tools for you") {
		t.Fatalf("hook deny = %v err=%v", decision, err)
	}

	// An auto-review turn reviews the call instead of prompting the user.
	allowedReviewer := &fakeGuardianReviewer{decision: state.DecisionApproved}
	decision, err = approve(t, newRouter(t, "approvals_reviewer = \"auto_review\"\n", "", allowedReviewer))
	if err != nil || decision != mcp.MCPToolApprovalApprove {
		t.Fatalf("guardian approval = %v err=%v", decision, err)
	}
	if len(allowedReviewer.actions) != 1 {
		t.Fatalf("guardian actions = %#v", allowedReviewer.actions)
	}
	action := allowedReviewer.actions[0]
	if action.Type != "mcp_tool_call" || action.Server != "server" || action.ToolName != "tool" {
		t.Fatalf("guardian MCP action = %#v", action)
	}
	arguments, _ := action.Extra["arguments"].(map[string]any)
	if arguments["path"] != "src/main.go" {
		t.Fatalf("guardian MCP arguments = %#v", action.Extra)
	}

	deniedReviewer := &fakeGuardianReviewer{decision: state.DecisionDenied, reason: "risky tool"}
	decision, err = approve(t, newRouter(t, "approvals_reviewer = \"auto_review\"\n", "", deniedReviewer))
	if decision != mcp.MCPToolApprovalDeny || err == nil || !strings.Contains(err.Error(), "risky tool") {
		t.Fatalf("guardian denial = %v err=%v", decision, err)
	}
}
