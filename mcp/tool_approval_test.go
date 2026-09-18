package mcp

import (
	"context"
	"testing"

	"codex_go/apps"
	"codex_go/sandbox"
	"codex_go/tool"
)

// TestToolExecutorHonorsCodexAppsPolicyLikeRust covers the codex_apps half of
// the gate: a tool the app configuration disables never runs, and an enabled
// tool uses the app approval mode instead of the custom-server mode.
func TestToolExecutorHonorsCodexAppsPolicyLikeRust(t *testing.T) {
	appConfig := func(values map[string]any) *apps.AppToolPolicyEvaluator {
		return apps.NewAppToolPolicyEvaluator(apps.AppsConfigFromValues(map[string]any{"apps": values}))
	}
	tests := []struct {
		name         string
		appPolicy    *apps.AppToolPolicyEvaluator
		annotations  map[string]any
		wantPrompted bool
		wantMode     apps.AppToolApproval
		wantBlocked  bool
	}{
		{
			name: "disabled tool is blocked",
			appPolicy: appConfig(map[string]any{
				"drive": map[string]any{"tools": map[string]any{"files/read": map[string]any{"enabled": false}}},
			}),
			wantBlocked: true,
		},
		{
			name:        "disabled app blocks its tools",
			appPolicy:   appConfig(map[string]any{"drive": map[string]any{"enabled": false}}),
			wantBlocked: true,
		},
		{
			name: "app approval mode prompts",
			appPolicy: appConfig(map[string]any{
				"drive": map[string]any{"default_tools_approval_mode": "prompt"},
			}),
			wantPrompted: true,
			wantMode:     apps.AppToolApprovalPrompt,
		},
		{
			name:      "approve mode skips the prompt",
			appPolicy: appConfig(map[string]any{"drive": map[string]any{"default_tools_approval_mode": "approve"}}),
		},
		{
			name:         "unconfigured app uses the hint policy",
			appPolicy:    appConfig(map[string]any{}),
			wantPrompted: true,
			wantMode:     apps.AppToolApprovalAuto,
		},
		{
			name:         "no evaluator keeps approvals server-driven",
			appPolicy:    nil,
			wantPrompted: false,
		},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			prompted := false
			handler := MCPToolApprovalHandlerFunc(func(_ context.Context, request *MCPToolApprovalRequest) (MCPToolApprovalOutcome, error) {
				prompted = true
				if testCase.wantMode != "" && request.ApprovalMode != testCase.wantMode {
					t.Errorf("approval mode = %q, want %q", request.ApprovalMode, testCase.wantMode)
				}
				return MCPToolApprovalOutcome{Decision: MCPToolApprovalApprove}, nil
			})
			executor := NewToolExecutor(&ToolExecutorOptions{
				Service:     NewMCPService(nil),
				ServerName:  RuntimeCodexAppsMCPServerName,
				ConnectorID: "drive",
				ToolName:    tool.NamespacedName(RuntimeCodexAppsMCPServerName, "files/read"),
				ToolInfo:    &MCPToolInfo{Name: "files/read", Annotations: testCase.annotations},
				ThreadID:    "thread-1",
				TurnID:      "turn-1",
				ToolApproval: &ToolApprovalOptions{
					Handler:        handler,
					ApprovalPolicy: sandbox.ApprovalOnRequest,
					AppPolicy:      testCase.appPolicy,
				},
			})
			denied, err := executor.approveToolCallIfNeeded(context.Background(), "call-1", nil)
			if err != nil {
				t.Fatalf("approveToolCallIfNeeded() error = %v", err)
			}
			if prompted != testCase.wantPrompted {
				t.Fatalf("prompted = %v, want %v", prompted, testCase.wantPrompted)
			}
			if testCase.wantBlocked {
				if denied == nil || denied.Success || denied.Body != MCPToolCallBlockedByAppConfigurationMessage {
					t.Fatalf("blocked output = %#v", denied)
				}
				return
			}
			if denied != nil {
				t.Fatalf("denied output = %#v, want nil", denied)
			}
		})
	}
}

func boolPtrMCPApproval(value bool) *bool { return &value }

func approvalModePtr(mode apps.AppToolApproval) *apps.AppToolApproval { return &mode }

func newToolApprovalTestService(values map[string]any) *MCPService {
	service := NewMCPService(nil)
	service.configs = map[string]ServerConfig{
		"docs": *ServerConfigFromValues(values),
	}
	return service
}

// TestMCPToolApprovalModeUsesServerDefaultWithToolOverrideLikeRust mirrors
// Rust custom_mcp_tool_approval_mode_uses_server_default_with_tool_override.
func TestMCPToolApprovalModeUsesServerDefaultWithToolOverrideLikeRust(t *testing.T) {
	service := newToolApprovalTestService(map[string]any{
		"command":                     "docs-server",
		"default_tools_approval_mode": "approve",
		"tools":                       map[string]any{"search": map[string]any{"approval_mode": "prompt"}},
	})
	tests := []struct {
		name   string
		server string
		tool   string
		want   apps.AppToolApproval
	}{
		{name: "server default", server: "docs", tool: "read", want: apps.AppToolApprovalApprove},
		{name: "tool override", server: "docs", tool: "search", want: apps.AppToolApprovalPrompt},
		{name: "unknown server", server: "unknown", tool: "search", want: apps.AppToolApprovalAuto},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			if got := service.ToolApprovalMode(testCase.server, testCase.tool); got != testCase.want {
				t.Fatalf("ToolApprovalMode(%q, %q) = %q, want %q", testCase.server, testCase.tool, got, testCase.want)
			}
		})
	}
}

// TestMCPToolApprovalModeDefaultsToAutoLikeRust pins Rust's
// AppToolApproval::Auto default for an unconfigured server.
func TestMCPToolApprovalModeDefaultsToAutoLikeRust(t *testing.T) {
	service := newToolApprovalTestService(map[string]any{"command": "docs-server"})
	if got := service.ToolApprovalMode("docs", "read"); got != apps.AppToolApprovalAuto {
		t.Fatalf("ToolApprovalMode() = %q, want auto", got)
	}
}

// TestRequiresMCPToolApprovalForModeLikeRust mirrors Rust
// requires_mcp_tool_approval_for_mode / requires_mcp_tool_approval.
func TestRequiresMCPToolApprovalForModeLikeRust(t *testing.T) {
	destructive := &RuntimeToolAnnotations{DestructiveHint: boolPtrMCPApproval(true)}
	readOnly := &RuntimeToolAnnotations{ReadOnlyHint: boolPtrMCPApproval(true)}
	openWorldOnly := &RuntimeToolAnnotations{
		ReadOnlyHint:    boolPtrMCPApproval(false),
		DestructiveHint: boolPtrMCPApproval(false),
		OpenWorldHint:   boolPtrMCPApproval(true),
	}
	safe := &RuntimeToolAnnotations{
		ReadOnlyHint:    boolPtrMCPApproval(false),
		DestructiveHint: boolPtrMCPApproval(false),
		OpenWorldHint:   boolPtrMCPApproval(false),
	}
	tests := []struct {
		name        string
		annotations *RuntimeToolAnnotations
		mode        apps.AppToolApproval
		want        bool
	}{
		{name: "auto destructive", annotations: destructive, mode: apps.AppToolApprovalAuto, want: true},
		{name: "auto read-only", annotations: readOnly, mode: apps.AppToolApprovalAuto, want: false},
		{name: "auto unannotated", annotations: nil, mode: apps.AppToolApprovalAuto, want: true},
		{name: "auto open-world", annotations: openWorldOnly, mode: apps.AppToolApprovalAuto, want: true},
		{name: "auto safe", annotations: safe, mode: apps.AppToolApprovalAuto, want: false},
		{name: "prompt read-only", annotations: readOnly, mode: apps.AppToolApprovalPrompt, want: true},
		{name: "writes read-only", annotations: readOnly, mode: apps.AppToolApprovalWrites, want: false},
		{name: "writes unannotated", annotations: nil, mode: apps.AppToolApprovalWrites, want: true},
		{name: "approve destructive", annotations: destructive, mode: apps.AppToolApprovalApprove, want: false},
		{name: "unset defaults to auto", annotations: readOnly, mode: apps.AppToolApproval(""), want: false},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			if got := RequiresMCPToolApprovalForMode(testCase.annotations, testCase.mode); got != testCase.want {
				t.Fatalf("RequiresMCPToolApprovalForMode(%+v, %q) = %v, want %v", testCase.annotations, testCase.mode, got, testCase.want)
			}
		})
	}
}

// TestMCPPermissionPromptAutoApprovedLikeRust mirrors Rust
// mcp_prompt_auto_approval_honors_approved_tools_in_all_permission_modes and
// mcp_prompt_auto_approval_honors_unrestricted_managed_profiles.
func TestMCPPermissionPromptAutoApprovedLikeRust(t *testing.T) {
	fullAccess := sandbox.FullAccessPermissionProfile()
	readOnly := sandbox.ReadOnlyPermissionProfile()
	policies := []sandbox.AskForApproval{
		sandbox.ApprovalUnlessTrusted,
		sandbox.ApprovalOnRequest,
		sandbox.ApprovalGranular,
		sandbox.ApprovalNever,
	}
	for _, policy := range policies {
		if !MCPPermissionPromptAutoApproved(policy, &readOnly, apps.AppToolApprovalApprove) {
			t.Fatalf("approve mode must auto-approve under policy %q", policy)
		}
	}
	tests := []struct {
		name    string
		policy  sandbox.AskForApproval
		profile *sandbox.PermissionProfile
		mode    apps.AppToolApproval
		want    bool
	}{
		{name: "auto on-request read-only", policy: sandbox.ApprovalOnRequest, profile: &readOnly, mode: apps.AppToolApprovalAuto, want: false},
		{name: "never full access", policy: sandbox.ApprovalNever, profile: &fullAccess, mode: apps.AppToolApprovalAuto, want: true},
		{name: "never read-only", policy: sandbox.ApprovalNever, profile: &readOnly, mode: apps.AppToolApprovalAuto, want: false},
		{name: "on-request full access", policy: sandbox.ApprovalOnRequest, profile: &fullAccess, mode: apps.AppToolApprovalAuto, want: false},
		{name: "never disabled profile", policy: sandbox.ApprovalNever, profile: &sandbox.PermissionProfile{Disabled: true}, mode: apps.AppToolApprovalAuto, want: true},
		{name: "never nil profile", policy: sandbox.ApprovalNever, profile: nil, mode: apps.AppToolApprovalAuto, want: false},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			if got := MCPPermissionPromptAutoApproved(testCase.policy, testCase.profile, testCase.mode); got != testCase.want {
				t.Fatalf("MCPPermissionPromptAutoApproved() = %v, want %v", got, testCase.want)
			}
		})
	}
}

// TestBuildMCPToolApprovalQuestionLikeRust mirrors Rust
// build_mcp_tool_approval_question, including the option gating and the
// question-override normalization.
func TestBuildMCPToolApprovalQuestionLikeRust(t *testing.T) {
	question := BuildMCPToolApprovalQuestion(
		"mcp_tool_call_approval_call-1",
		"docs",
		"search",
		"",
		MCPToolApprovalPromptOptions{AllowSessionRemember: true, AllowPersistentApproval: true},
		"",
	)
	if question.ID != "mcp_tool_call_approval_call-1" || question.Header != MCPToolApprovalHeader {
		t.Fatalf("question identity = %#v", question)
	}
	if question.Question != `Allow the docs MCP server to run tool "search"?` {
		t.Fatalf("question = %q", question.Question)
	}
	wantLabels := []string{
		MCPToolApprovalAccept,
		MCPToolApprovalAcceptForSession,
		MCPToolApprovalAcceptAndRemember,
		MCPToolApprovalCancel,
	}
	if len(question.Options) != len(wantLabels) {
		t.Fatalf("options = %#v", question.Options)
	}
	for index, want := range wantLabels {
		if question.Options[index].Label != want {
			t.Fatalf("option %d = %q, want %q", index, question.Options[index].Label, want)
		}
	}
	if question.IsOther || question.IsSecret {
		t.Fatalf("question must not offer free text or secret input: %#v", question)
	}

	sessionOnly := BuildMCPToolApprovalQuestion(
		"id", "docs", "search", "",
		MCPToolApprovalPromptOptions{AllowSessionRemember: true},
		"",
	)
	if len(sessionOnly.Options) != 3 || sessionOnly.Options[1].Label != MCPToolApprovalAcceptForSession {
		t.Fatalf("session-only options = %#v", sessionOnly.Options)
	}

	plain := BuildMCPToolApprovalQuestion("id", "docs", "search", "", MCPToolApprovalPromptOptions{}, "")
	if len(plain.Options) != 2 || plain.Options[1].Label != MCPToolApprovalCancel {
		t.Fatalf("plain options = %#v", plain.Options)
	}

	override := BuildMCPToolApprovalQuestion(
		"id", "docs", "search", "",
		MCPToolApprovalPromptOptions{},
		"Allow the docs server to run this tool??",
	)
	if override.Question != "Allow the docs server to run this tool?" {
		t.Fatalf("override question = %q", override.Question)
	}

	connector := BuildMCPToolApprovalQuestion(
		"id", "codex_apps", "search", "Some App",
		MCPToolApprovalPromptOptions{},
		"",
	)
	if connector.Question != `Allow Some App to run tool "search"?` {
		t.Fatalf("connector question = %q", connector.Question)
	}
	codexApps := BuildMCPToolApprovalQuestion("id", "codex_apps", "search", "", MCPToolApprovalPromptOptions{}, "")
	if codexApps.Question != `Allow this app to run tool "search"?` {
		t.Fatalf("codex apps question = %q", codexApps.Question)
	}
}

// TestParseMCPToolApprovalResponseLikeRust mirrors Rust
// parse_mcp_tool_approval_response: session beats remember beats accept, and an
// unanswerable question aborts.
func TestParseMCPToolApprovalResponseLikeRust(t *testing.T) {
	const questionID = "mcp_tool_call_approval_call-1"
	tests := []struct {
		name     string
		response *tool.UserInputResponse
		want     MCPToolApprovalDecision
	}{
		{name: "nil response", response: nil, want: MCPToolApprovalDeny},
		{name: "missing answer", response: &tool.UserInputResponse{Answers: map[string]string{}}, want: MCPToolApprovalDeny},
		{
			name:     "accept",
			response: &tool.UserInputResponse{Answers: map[string]string{questionID: MCPToolApprovalAccept}},
			want:     MCPToolApprovalApprove,
		},
		{
			name:     "accept for session",
			response: &tool.UserInputResponse{Answers: map[string]string{questionID: MCPToolApprovalAcceptForSession}},
			want:     MCPToolApprovalApproveForSession,
		},
		{
			name:     "accept and remember",
			response: &tool.UserInputResponse{Answers: map[string]string{questionID: MCPToolApprovalAcceptAndRemember}},
			want:     MCPToolApprovalApproveAndRemember,
		},
		{
			name:     "cancel",
			response: &tool.UserInputResponse{Answers: map[string]string{questionID: MCPToolApprovalCancel}},
			want:     MCPToolApprovalDeny,
		},
		{
			name: "session wins over accept",
			response: &tool.UserInputResponse{StructuredAnswers: map[string][]string{
				questionID: {MCPToolApprovalAccept, MCPToolApprovalAcceptForSession},
			}},
			want: MCPToolApprovalApproveForSession,
		},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			if got := ParseMCPToolApprovalResponse(testCase.response, questionID); got != testCase.want {
				t.Fatalf("ParseMCPToolApprovalResponse() = %v, want %v", got, testCase.want)
			}
		})
	}
}

// TestParseMCPToolApprovalElicitationResponseLikeRust mirrors Rust
// parse_mcp_tool_approval_elicitation_response.
func TestParseMCPToolApprovalElicitationResponseLikeRust(t *testing.T) {
	const questionID = "mcp_tool_call_approval_call-1"
	tests := []struct {
		name    string
		action  string
		meta    any
		content any
		want    MCPToolApprovalDecision
	}{
		{name: "accept", action: "accept", want: MCPToolApprovalApprove},
		{
			name:   "accept for session",
			action: "accept",
			meta:   map[string]any{"persist": "session"},
			want:   MCPToolApprovalApproveForSession,
		},
		{
			name:   "accept and remember",
			action: "accept",
			meta:   map[string]any{"persist": "always"},
			want:   MCPToolApprovalApproveAndRemember,
		},
		{
			name:    "accept with a user-input answer",
			action:  "accept",
			content: map[string]any{questionID: MCPToolApprovalAcceptForSession},
			want:    MCPToolApprovalApproveForSession,
		},
		{
			name:    "accept with an unrelated answer stays approved",
			action:  "accept",
			content: map[string]any{"other": MCPToolApprovalAccept},
			want:    MCPToolApprovalApprove,
		},
		{name: "decline rejects", action: "decline", want: MCPToolApprovalReject},
		{name: "cancel aborts", action: "cancel", want: MCPToolApprovalDeny},
		{name: "unknown aborts", action: "weird", want: MCPToolApprovalDeny},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			if got := ParseMCPToolApprovalElicitationResponse(testCase.action, testCase.meta, testCase.content, questionID); got != testCase.want {
				t.Fatalf("ParseMCPToolApprovalElicitationResponse() = %v, want %v", got, testCase.want)
			}
		})
	}
}

// TestToolExecutorUsesTheRejectionMessageLikeRust covers the distinct model
// text for a rejected (declined) call versus an aborted one.
func TestToolExecutorUsesTheRejectionMessageLikeRust(t *testing.T) {
	service := newToolApprovalTestService(map[string]any{"command": "docs-server"})
	executor := NewToolExecutor(&ToolExecutorOptions{
		Service:    service,
		ServerName: "docs",
		ToolName:   tool.NamespacedName("docs", "search"),
		ToolInfo:   &MCPToolInfo{Name: "search"},
		ThreadID:   "thread-1",
		TurnID:     "turn-1",
		ToolApproval: &ToolApprovalOptions{
			Handler: MCPToolApprovalHandlerFunc(func(context.Context, *MCPToolApprovalRequest) (MCPToolApprovalOutcome, error) {
				return MCPToolApprovalOutcome{Decision: MCPToolApprovalReject}, nil
			}),
			ApprovalPolicy: sandbox.ApprovalOnRequest,
		},
	})
	denied, err := executor.approveToolCallIfNeeded(context.Background(), "call-1", nil)
	if err != nil {
		t.Fatalf("approveToolCallIfNeeded() error = %v", err)
	}
	if denied == nil || denied.Success || denied.Body != MCPToolApprovalRejectedMessage {
		t.Fatalf("rejection output = %#v", denied)
	}
}

// TestToolExecutorSurfacesHandlerMessagesLikeRust mirrors Rust #46066: a denied
// decision may carry the handler's own message (the subagent handoff guidance),
// which replaces the default rejection text the model sees.
func TestToolExecutorSurfacesHandlerMessagesLikeRust(t *testing.T) {
	service := newToolApprovalTestService(map[string]any{"command": "docs-server"})
	executor := NewToolExecutor(&ToolExecutorOptions{
		Service:    service,
		ServerName: "docs",
		ToolName:   tool.NamespacedName("docs", "search"),
		ToolInfo:   &MCPToolInfo{Name: "search"},
		ThreadID:   "thread-1",
		TurnID:     "turn-1",
		ToolApproval: &ToolApprovalOptions{
			Handler: MCPToolApprovalHandlerFunc(func(context.Context, *MCPToolApprovalRequest) (MCPToolApprovalOutcome, error) {
				return MCPToolApprovalOutcome{Decision: MCPToolApprovalDeny, Message: MCPElicitationHandoffMessage}, nil
			}),
			ApprovalPolicy: sandbox.ApprovalOnRequest,
		},
	})
	denied, err := executor.approveToolCallIfNeeded(context.Background(), "call-1", nil)
	if err != nil {
		t.Fatalf("approveToolCallIfNeeded() error = %v", err)
	}
	if denied == nil || denied.Success || denied.Body != MCPElicitationHandoffMessage {
		t.Fatalf("handoff output = %#v", denied)
	}
}

// TestNormalizeMCPToolApprovalDecisionLikeRust mirrors Rust
// normalize_approval_decision_for_mode.
func TestNormalizeMCPToolApprovalDecisionLikeRust(t *testing.T) {
	tests := []struct {
		mode     apps.AppToolApproval
		decision MCPToolApprovalDecision
		want     MCPToolApprovalDecision
	}{
		{apps.AppToolApprovalPrompt, MCPToolApprovalApproveForSession, MCPToolApprovalApprove},
		{apps.AppToolApprovalPrompt, MCPToolApprovalApproveAndRemember, MCPToolApprovalApprove},
		{apps.AppToolApprovalWrites, MCPToolApprovalApproveForSession, MCPToolApprovalApprove},
		{apps.AppToolApprovalAuto, MCPToolApprovalApproveForSession, MCPToolApprovalApproveForSession},
		{apps.AppToolApprovalAuto, MCPToolApprovalApproveAndRemember, MCPToolApprovalApproveAndRemember},
		{apps.AppToolApprovalAuto, MCPToolApprovalDeny, MCPToolApprovalDeny},
	}
	for _, testCase := range tests {
		if got := NormalizeMCPToolApprovalDecision(testCase.decision, testCase.mode); got != testCase.want {
			t.Fatalf("NormalizeMCPToolApprovalDecision(%v, %q) = %v, want %v", testCase.decision, testCase.mode, got, testCase.want)
		}
	}
}

// TestMCPSessionToolApprovalKeyLikeRust mirrors Rust
// session_mcp_tool_approval_key: only Auto mode offers a remembered approval.
func TestMCPSessionToolApprovalKeyLikeRust(t *testing.T) {
	if _, ok := MCPSessionToolApprovalKey(apps.AppToolApprovalAuto, "docs", "search"); !ok {
		t.Fatal("auto mode must offer a session key")
	}
	for _, mode := range []apps.AppToolApproval{apps.AppToolApprovalPrompt, apps.AppToolApprovalWrites, apps.AppToolApprovalApprove} {
		if _, ok := MCPSessionToolApprovalKey(mode, "docs", "search"); ok {
			t.Fatalf("mode %q must not offer a session key", mode)
		}
	}
	if _, ok := MCPSessionToolApprovalKey(apps.AppToolApprovalAuto, "", "search"); ok {
		t.Fatal("empty server must not offer a session key")
	}
}

// TestToolExecutorGatesCustomMCPToolCallsLikeRust covers the executor gate: an
// unapproved call is denied with Rust's message, while read-only tools,
// approve-mode servers, and auto-approving policies never prompt.
func TestToolExecutorGatesCustomMCPToolCallsLikeRust(t *testing.T) {
	readOnlyAnnotations := map[string]any{"readOnlyHint": true}
	fullAccess := sandbox.FullAccessPermissionProfile()

	tests := []struct {
		name         string
		values       map[string]any
		annotations  map[string]any
		policy       sandbox.AskForApproval
		profile      *sandbox.PermissionProfile
		handler      MCPToolApprovalHandlerFunc
		wantPrompted bool
		wantDenied   bool
	}{
		{
			name:   "auto unannotated prompts",
			values: map[string]any{"command": "docs-server"},
			handler: func(context.Context, *MCPToolApprovalRequest) (MCPToolApprovalOutcome, error) {
				return MCPToolApprovalOutcome{Decision: MCPToolApprovalDeny}, nil
			},
			policy:       sandbox.ApprovalOnRequest,
			wantPrompted: true,
			wantDenied:   true,
		},
		{
			name:        "auto read-only does not prompt",
			values:      map[string]any{"command": "docs-server"},
			annotations: readOnlyAnnotations,
			handler: func(context.Context, *MCPToolApprovalRequest) (MCPToolApprovalOutcome, error) {
				t.Error("handler must not be called for a read-only tool")
				return MCPToolApprovalOutcome{Decision: MCPToolApprovalDeny}, nil
			},
			policy: sandbox.ApprovalOnRequest,
		},
		{
			name:        "approve mode does not prompt",
			values:      map[string]any{"command": "docs-server", "default_tools_approval_mode": "approve"},
			annotations: nil,
			handler: func(context.Context, *MCPToolApprovalRequest) (MCPToolApprovalOutcome, error) {
				t.Error("handler must not be called in approve mode")
				return MCPToolApprovalOutcome{Decision: MCPToolApprovalDeny}, nil
			},
			policy: sandbox.ApprovalOnRequest,
		},
		{
			name:        "never policy full access does not prompt",
			values:      map[string]any{"command": "docs-server"},
			annotations: nil,
			handler: func(context.Context, *MCPToolApprovalRequest) (MCPToolApprovalOutcome, error) {
				t.Error("handler must not be called under never + full access")
				return MCPToolApprovalOutcome{Decision: MCPToolApprovalDeny}, nil
			},
			policy:  sandbox.ApprovalNever,
			profile: &fullAccess,
		},
		{
			name:        "prompt mode accepts",
			values:      map[string]any{"command": "docs-server", "default_tools_approval_mode": "prompt"},
			annotations: readOnlyAnnotations,
			handler: func(_ context.Context, request *MCPToolApprovalRequest) (MCPToolApprovalOutcome, error) {
				if request.ApprovalMode != apps.AppToolApprovalPrompt || request.Server != "docs" || request.Tool != "search" {
					t.Errorf("approval request = %#v", request)
				}
				if request.SessionKey != nil || request.AllowSessionRemember {
					t.Errorf("prompt mode must not offer a remembered approval: %#v", request)
				}
				return MCPToolApprovalOutcome{Decision: MCPToolApprovalApprove}, nil
			},
			policy:       sandbox.ApprovalOnRequest,
			wantPrompted: true,
		},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			prompted := false
			handler := MCPToolApprovalHandlerFunc(func(ctx context.Context, request *MCPToolApprovalRequest) (MCPToolApprovalOutcome, error) {
				prompted = true
				return testCase.handler(ctx, request)
			})
			service := newToolApprovalTestService(testCase.values)
			executor := NewToolExecutor(&ToolExecutorOptions{
				Service:    service,
				ServerName: "docs",
				ToolName:   tool.NamespacedName("docs", "search"),
				ToolInfo: &MCPToolInfo{
					Name:        "search",
					Annotations: testCase.annotations,
				},
				ThreadID: "thread-1",
				TurnID:   "turn-1",
				ToolApproval: &ToolApprovalOptions{
					Handler:        handler,
					ApprovalPolicy: testCase.policy,
					PermissionProfileForServer: func(string) *sandbox.PermissionProfile {
						return testCase.profile
					},
				},
			})
			denied, err := executor.approveToolCallIfNeeded(context.Background(), "call-1", map[string]any{"q": "x"})
			if err != nil {
				t.Fatalf("approveToolCallIfNeeded() error = %v", err)
			}
			if prompted != testCase.wantPrompted {
				t.Fatalf("prompted = %v, want %v", prompted, testCase.wantPrompted)
			}
			if testCase.wantDenied {
				if denied == nil || denied.Success || denied.Body != MCPToolApprovalDeniedMessage {
					t.Fatalf("denied output = %#v", denied)
				}
				return
			}
			if denied != nil {
				t.Fatalf("denied output = %#v, want nil", denied)
			}
		})
	}
}
