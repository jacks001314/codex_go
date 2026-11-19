package appserver

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"codex_go/apps"
	"codex_go/config"
	"codex_go/mcp"
	"codex_go/sandbox"
)

func appToolReviewerConfig() *config.Config {
	return &config.Config{Values: map[string]any{
		"apps": map[string]any{
			"_default": map[string]any{"approvals_reviewer": "user"},
			"calendar": map[string]any{
				"approvals_reviewer": "auto_review",
				"links": map[string]any{
					"link-guardian": map[string]any{"approvals_reviewer": "guardian_subagent"},
				},
				"tools": map[string]any{
					"calendar_create_event": map[string]any{"approvals_reviewer": "user"},
					"Create event":          map[string]any{"approvals_reviewer": "auto_review"},
				},
			},
		},
	}}
}

// TestMCPApprovalsReviewerUsesAppToolLayerLikeRust mirrors Rust #62360: the
// per-tool approvals_reviewer wins over the account link, the connector, the
// apps default and the thread-level reviewer, and the raw tool name wins over
// the tool title.
func TestMCPApprovalsReviewerUsesAppToolLayerLikeRust(t *testing.T) {
	for _, tc := range []struct {
		name      string
		server    string
		connector string
		link      string
		tool      string
		title     string
		want      string
	}{
		{name: "tool name over link", server: "codex_apps", connector: "calendar", link: "link-guardian", tool: "calendar_create_event", title: "Create event", want: "user"},
		{name: "tool title fallback", server: "codex_apps", connector: "calendar", link: "link-guardian", tool: "calendar_create_event_v2", title: "Create event", want: "auto_review"},
		{name: "unknown tool keeps link", server: "codex_apps", connector: "calendar", link: "link-guardian", tool: "calendar_delete_event", title: "Delete event", want: "guardian_subagent"},
		{name: "unknown tool keeps connector", server: "codex_apps", connector: "calendar", tool: "calendar_delete_event", title: "Delete event", want: "auto_review"},
		{name: "unknown connector keeps default", server: "codex_apps", connector: "drive", tool: "calendar_create_event", title: "Create event", want: "user"},
		{name: "custom server ignores app tools", server: "custom_server", connector: "calendar", link: "link-guardian", tool: "calendar_create_event", title: "Create event", want: "user"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := mcpApprovalsReviewerForElicitation(appToolReviewerConfig(), "user", tc.server, tc.connector, tc.link, "", tc.tool, tc.title); got != tc.want {
				t.Fatalf("reviewer = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestMCPApprovalsReviewerManagedRequirementsLikeRust pins the managed
// requirements ladder from Rust #62360: an app reviewer the requirements
// disallow is skipped, the thread-level reviewer is validated the same way, and
// the requirement's own value is the last resort.
func TestMCPApprovalsReviewerManagedRequirementsLikeRust(t *testing.T) {
	for _, tc := range []struct {
		name     string
		allowed  []config.ApprovalsReviewer
		fallback string
		want     string
	}{
		{name: "requirements allow the tool reviewer", allowed: []config.ApprovalsReviewer{"user", "auto_review"}, want: "user"},
		{name: "requirements allow the thread reviewer", allowed: []config.ApprovalsReviewer{"auto_review"}, fallback: "auto_review", want: "auto_review"},
		{name: "requirements force their own value", allowed: []config.ApprovalsReviewer{"guardian_subagent"}, fallback: "guardian_subagent", want: "guardian_subagent"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := appToolReviewerConfig()
			cfg.Requirements = &config.ConfigRequirements{AllowedApprovalsReviewers: tc.allowed}
			defaultReviewer := "user"
			if tc.name == "requirements allow the thread reviewer" {
				defaultReviewer = "auto_review"
			}
			if got := mcpApprovalsReviewerForElicitation(cfg, defaultReviewer, "codex_apps", "calendar", "link-guardian", "", "calendar_create_event", "Create event"); got != tc.want {
				t.Fatalf("reviewer = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestMCPToolApprovalReviewerResolvesAppToolLikeRust exercises the approval
// handler end to end: a config.toml per-tool approvals_reviewer, matched by the
// request's raw tool name and then its title, selects the reviewer through the
// thread's effective apps config.
func TestMCPToolApprovalReviewerResolvesAppToolLikeRust(t *testing.T) {
	home := t.TempDir()
	configToml := `
approvals_reviewer = "user"

[apps.calendar]
approvals_reviewer = "auto_review"

[apps.calendar.links.link-guardian]
approvals_reviewer = "guardian_subagent"

[apps.calendar.tools.calendar_create_event]
approvals_reviewer = "user"

[apps.calendar.tools."Create event"]
approvals_reviewer = "auto_review"
`
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(configToml), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	router := NewRuntimeRouter(RuntimeServices{Config: config.NewConfigService(home)})
	t.Cleanup(func() { _ = router.Close() })
	handler := &appserverMCPToolApprovalHandler{router: router, threadID: "thread-tool", turnID: "turn-tool"}
	request := func(toolName, toolTitle string) *mcp.MCPToolApprovalRequest {
		return &mcp.MCPToolApprovalRequest{
			Server:       mcp.RuntimeCodexAppsMCPServerName,
			ConnectorID:  "calendar",
			LinkID:       "link-guardian",
			Tool:         toolName,
			ToolTitle:    toolTitle,
			ApprovalMode: apps.AppToolApprovalPrompt,
		}
	}
	if got := handler.mcpToolApprovalReviewer(request("calendar_create_event", "Create event")); got.RoutesToGuardian() {
		t.Fatalf("tool reviewer = %q, want the user reviewer", got)
	}
	if got := handler.mcpToolApprovalReviewer(request("calendar_create_event_v2", "Create event")); !got.RoutesToGuardian() {
		t.Fatalf("title reviewer = %q, want auto_review", got)
	}
	if got := handler.mcpToolApprovalReviewer(request("calendar_delete_event", "Delete event")); !got.RoutesToGuardian() {
		t.Fatalf("link reviewer = %q, want guardian_subagent", got)
	}
}

// TestMCPElicitationUsesVerifiedAppToolReviewerLikeRust covers the elicitation
// path: only a host-registered outer invocation matching the elicitation's
// connector and tool contributes the per-tool reviewer identity.
func TestMCPElicitationUsesVerifiedAppToolReviewerLikeRust(t *testing.T) {
	service := mcp.NewMCPService(nil)
	release := service.RegisterToolInvocation("codex_apps", "call-1", mcp.MCPInvocationMetadata{
		ServerName:  "codex_apps",
		ToolName:    "calendar_create_event",
		ToolTitle:   "Create event",
		ConnectorID: "calendar",
	})
	defer release()
	seenTool, seenTitle := "sentinel", "sentinel"
	handler := &appserverMCPElicitationHandler{
		service: service,
		authority: func(threadID, serverName, connectorID, toolName, toolTitle string) mcpElicitationAuthority {
			if threadID != "thread-1" || serverName != mcp.RuntimeCodexAppsMCPServerName || connectorID != "calendar" {
				t.Fatalf("authority scope = %s/%s/%s", threadID, serverName, connectorID)
			}
			seenTool, seenTitle = toolName, toolTitle
			return mcpElicitationAuthority{ApprovalPolicy: sandbox.ApprovalOnRequest, ApprovalsReviewer: "user", AllowsMCPElicitations: true}
		},
	}
	request := func(toolName string) *mcp.MCPElicitationRequest {
		return &mcp.MCPElicitationRequest{
			ServerName:    mcp.RuntimeCodexAppsMCPServerName,
			ThreadID:      "thread-1",
			TurnID:        "turn-1",
			ElicitationID: "elicitation-1",
			Method:        "elicitation/create",
			Meta: map[string]any{
				"codex_request_type":  "approval_request",
				"codex_approval_kind": "mcp_tool_call",
				"connector_id":        "calendar",
				"tool_name":           toolName,
				"_codex_apps":         map[string]any{"call_id": "call-1"},
			},
		}
	}
	if _, err := handler.HandleMCPElicitation(context.Background(), request("calendar_create_event")); err != nil {
		t.Fatalf("HandleMCPElicitation() error = %v", err)
	}
	if seenTool != "calendar_create_event" || seenTitle != "Create event" {
		t.Fatalf("verified tool identity = %q/%q, want calendar_create_event/Create event", seenTool, seenTitle)
	}
	seenTool, seenTitle = "sentinel", "sentinel"
	if _, err := handler.HandleMCPElicitation(context.Background(), request("calendar_delete_event")); err != nil {
		t.Fatalf("HandleMCPElicitation() error = %v", err)
	}
	if seenTool != "" || seenTitle != "" {
		t.Fatalf("mismatched tool identity = %q/%q, want none", seenTool, seenTitle)
	}
}
