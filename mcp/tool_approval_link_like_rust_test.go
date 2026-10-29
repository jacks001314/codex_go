package mcp

import (
	"context"
	"strings"
	"testing"

	"codex_go/apps"
	"codex_go/sandbox"
	"codex_go/tool"
)

// TestToolApprovalUsesAppLinkSettingsLikeRust mirrors Rust #42056/#42133: the
// resolved app link selects its own default_tools_approval_mode and scopes the
// remembered approval key, so another link asks again.
func TestToolApprovalUsesAppLinkSettingsLikeRust(t *testing.T) {
	appPolicy := func() *apps.AppToolPolicyEvaluator {
		values := map[string]any{
			"apps": map[string]any{
				"calendar": map[string]any{
					"default_tools_approval_mode": "prompt",
					"links": map[string]any{
						"link-a": map[string]any{"default_tools_approval_mode": "approve"},
						"link-b": map[string]any{"default_tools_approval_mode": "prompt"},
					},
				},
			},
		}
		return apps.NewAppToolPolicyEvaluator(apps.AppsConfigFromValues(values))
	}
	run := func(t *testing.T, linkID string) (*MCPToolApprovalRequest, bool) {
		t.Helper()
		var captured *MCPToolApprovalRequest
		executor := NewToolExecutor(&ToolExecutorOptions{
			Service:     NewMCPService(nil),
			ServerName:  RuntimeCodexAppsMCPServerName,
			ConnectorID: "calendar",
			ToolName:    tool.NamespacedName(RuntimeCodexAppsMCPServerName, "create_event"),
			ToolInfo: &MCPToolInfo{
				Name: "create_event",
				Meta: map[string]any{"_codex_apps": map[string]any{"link_id": "link-a"}},
			},
			ToolApproval: &ToolApprovalOptions{
				ApprovalPolicy: sandbox.ApprovalOnRequest,
				AppPolicy:      appPolicy(),
				Handler: MCPToolApprovalHandlerFunc(func(_ context.Context, request *MCPToolApprovalRequest) (MCPToolApprovalOutcome, error) {
					captured = request
					return MCPToolApprovalOutcome{Decision: MCPToolApprovalApprove}, nil
				}),
			},
		})
		denied, err := executor.approveToolCallIfNeeded(context.Background(), "call-1", map[string]any{}, linkID)
		if err != nil {
			t.Fatalf("approveToolCallIfNeeded() error = %v", err)
		}
		if denied != nil {
			t.Fatalf("denied output = %#v", denied)
		}
		return captured, captured != nil
	}

	// link-a approves without prompting.
	if request, prompted := run(t, "link-a"); prompted {
		t.Fatalf("link-a prompted: %#v", request)
	}
	// link-b keeps its own prompt setting and carries its link in the key.
	request, prompted := run(t, "link-b")
	if !prompted || request == nil {
		t.Fatal("link-b must prompt")
	}
	if request.LinkID != "link-b" {
		t.Fatalf("request LinkID = %q", request.LinkID)
	}
	if request.ApprovalMode != apps.AppToolApprovalPrompt {
		t.Fatalf("approval mode = %q, want prompt", request.ApprovalMode)
	}
	if request.SessionKey != nil {
		t.Fatalf("prompt mode must not offer a session key: %#v", request.SessionKey)
	}

	// Auto mode scopes the remembered key by link.
	keyA, ok := MCPSessionToolApprovalKey(apps.AppToolApprovalAuto, "codex_apps", "create_event", "calendar", "link-a")
	if !ok {
		t.Fatal("auto mode must offer a session key")
	}
	keyB, _ := MCPSessionToolApprovalKey(apps.AppToolApprovalAuto, "codex_apps", "create_event", "calendar", "link-b")
	keyNone, _ := MCPSessionToolApprovalKey(apps.AppToolApprovalAuto, "codex_apps", "create_event", "calendar", "")
	if keyA == keyB || keyA == keyNone {
		t.Fatalf("link-scoped keys must differ: %#v %#v %#v", keyA, keyB, keyNone)
	}
	if keyA.LinkID != "link-a" || keyNone.LinkID != "" {
		t.Fatalf("keys = %#v %#v", keyA, keyNone)
	}
	keyOtherConnector, _ := MCPSessionToolApprovalKey(apps.AppToolApprovalAuto, "codex_apps", "create_event", "drive", "link-a")
	if keyOtherConnector == keyA || keyOtherConnector.ConnectorID != "drive" {
		t.Fatalf("connector-scoped keys must differ: %#v %#v", keyOtherConnector, keyA)
	}

	// An auto-mode Apps call for a link reports a session key carrying it.
	var autoRequest *MCPToolApprovalRequest
	executor := NewToolExecutor(&ToolExecutorOptions{
		Service:     NewMCPService(nil),
		ServerName:  RuntimeCodexAppsMCPServerName,
		ConnectorID: "calendar",
		ToolName:    tool.NamespacedName(RuntimeCodexAppsMCPServerName, "create_event"),
		ToolInfo:    &MCPToolInfo{Name: "create_event"},
		ToolApproval: &ToolApprovalOptions{
			ApprovalPolicy: sandbox.ApprovalOnRequest,
			AppPolicy:      apps.NewAppToolPolicyEvaluator(apps.AppsConfigFromValues(map[string]any{})),
			Handler: MCPToolApprovalHandlerFunc(func(_ context.Context, request *MCPToolApprovalRequest) (MCPToolApprovalOutcome, error) {
				autoRequest = request
				return MCPToolApprovalOutcome{Decision: MCPToolApprovalApproveForSession}, nil
			}),
		},
	})
	if _, err := executor.approveToolCallIfNeeded(context.Background(), "call-2", map[string]any{}, "implicit_link::acct-1"); err != nil {
		t.Fatalf("approveToolCallIfNeeded() error = %v", err)
	}
	if autoRequest == nil || autoRequest.SessionKey == nil || autoRequest.SessionKey.LinkID != "implicit_link::acct-1" {
		t.Fatalf("auto approval request = %#v", autoRequest)
	}
	if autoRequest.SessionKey.ConnectorID != "calendar" {
		t.Fatalf("session key connector = %q, want calendar", autoRequest.SessionKey.ConnectorID)
	}
	if !IsImplicitAppLinkID(autoRequest.LinkID) || IsImplicitAppLinkID("link-a") {
		t.Fatalf("implicit detection for %q", autoRequest.LinkID)
	}
	if !strings.HasPrefix(ImplicitAppLinkIDPrefix, "implicit_link") {
		t.Fatalf("prefix = %q", ImplicitAppLinkIDPrefix)
	}
}
