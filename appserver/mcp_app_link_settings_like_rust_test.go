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
	"codex_go/tool"
)

// TestMCPApprovalsReviewerUsesAppLinkLayerLikeRust mirrors Rust #42056: the
// account link's approvals_reviewer wins over the connector's, the apps
// default, and the thread-level reviewer, but only when the managed
// requirements allow it.
func TestMCPApprovalsReviewerUsesAppLinkLayerLikeRust(t *testing.T) {
	newConfig := func() *config.Config {
		return &config.Config{Values: map[string]any{
			"approvals_reviewer": "user",
			"apps": map[string]any{
				"_default": map[string]any{"approvals_reviewer": "user"},
				"calendar": map[string]any{
					"approvals_reviewer": "user",
					"links": map[string]any{
						"link-auto":  map[string]any{"approvals_reviewer": "auto_review"},
						"link-plain": map[string]any{},
					},
				},
			},
		}}
	}
	for _, tc := range []struct {
		name      string
		server    string
		connector string
		link      string
		want      string
	}{
		{name: "link overrides connector", server: "codex_apps", connector: "calendar", link: "link-auto", want: "auto_review"},
		{name: "link without reviewer falls back", server: "codex_apps", connector: "calendar", link: "link-plain", want: "user"},
		{name: "unknown link falls back", server: "codex_apps", connector: "calendar", link: "link-unknown", want: "user"},
		{name: "no link keeps connector", server: "codex_apps", connector: "calendar", want: "user"},
		{name: "custom server ignores link", server: "custom_server", connector: "calendar", link: "link-auto", want: "user"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := mcpApprovalsReviewerForElicitation(newConfig(), "user", tc.server, tc.connector, tc.link, ""); got != tc.want {
				t.Fatalf("reviewer = %q, want %q", got, tc.want)
			}
		})
	}

	// Managed requirements that only allow the thread reviewer keep the link
	// from routing to Guardian.
	restricted := newConfig()
	restricted.Requirements = &config.ConfigRequirements{AllowedApprovalsReviewers: []config.ApprovalsReviewer{config.ApprovalsReviewer("user")}}
	if got := mcpApprovalsReviewerForElicitation(restricted, "user", "codex_apps", "calendar", "link-auto", ""); got != "user" {
		t.Fatalf("restricted reviewer = %q, want user", got)
	}
}

// TestMCPToolApprovalReviewerResolvesAppLinkLikeRust exercises the approval
// handler itself: the request's link selects the reviewer through the
// thread's effective apps config.
func TestMCPToolApprovalReviewerResolvesAppLinkLikeRust(t *testing.T) {
	home := t.TempDir()
	configToml := `
approvals_reviewer = "user"

[apps.calendar]
approvals_reviewer = "user"

[apps.calendar.links.link-auto]
approvals_reviewer = "auto_review"
`
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(configToml), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	router := NewRuntimeRouter(RuntimeServices{Config: config.NewConfigService(home)})
	t.Cleanup(func() { _ = router.Close() })
	handler := &appserverMCPToolApprovalHandler{router: router, threadID: "thread-link", turnID: "turn-link"}
	request := func(linkID string) *mcp.MCPToolApprovalRequest {
		return &mcp.MCPToolApprovalRequest{Server: mcp.RuntimeCodexAppsMCPServerName, ConnectorID: "calendar", LinkID: linkID}
	}
	if got := handler.mcpToolApprovalReviewer(request("link-auto")); !got.RoutesToGuardian() {
		t.Fatalf("link reviewer = %q, want auto_review", got)
	}
	if got := handler.mcpToolApprovalReviewer(request("link-plain")); got.RoutesToGuardian() {
		t.Fatalf("plain link reviewer = %q, want user", got)
	}
}

// TestMCPToolApprovalElicitationCarriesAppLinkLikeRust mirrors Rust #42134: the
// elicited approval meta names the selected link and whether it is implicit.
func TestMCPToolApprovalElicitationCarriesAppLinkLikeRust(t *testing.T) {
	run := func(t *testing.T, linkID string) map[string]any {
		t.Helper()
		router := NewRuntimeRouter(RuntimeServices{})
		t.Cleanup(func() { _ = router.Close() })
		var params *MCPElicitationRequestParams
		router.SetServerRequestSink(ServerRequestSinkFunc(func(request *ServerRequest) {
			params, _ = request.Params.(*MCPElicitationRequestParams)
			_, _ = router.requireServerRequests().Resolve(OK(request.ID, &MCPElicitationRequestResponse{Action: MCPElicitationActionAccept}))
		}))
		handler := &appserverMCPToolApprovalHandler{
			router: router,
			responder: func(context.Context, string, *tool.RequestUserInputArgs) (*tool.UserInputResponse, error) {
				t.Error("the elicitation transport must not fall back to user input")
				return nil, nil
			},
			threadID:           "thread-link",
			turnID:             "turn-link",
			approvalPolicy:     sandbox.ApprovalOnRequest,
			elicitationEnabled: true,
		}
		outcome, err := handler.ApproveMCPToolCall(context.Background(), &mcp.MCPToolApprovalRequest{
			Server:       mcp.RuntimeCodexAppsMCPServerName,
			Tool:         "create_event",
			ConnectorID:  "calendar",
			LinkID:       linkID,
			ApprovalMode: apps.AppToolApprovalPrompt,
			CallID:       "call-link",
		})
		if err != nil {
			t.Fatalf("ApproveMCPToolCall() error = %v", err)
		}
		if outcome.Decision != mcp.MCPToolApprovalApprove {
			t.Fatalf("decision = %v, want approve", outcome.Decision)
		}
		if params == nil {
			t.Fatal("no elicitation request was sent")
		}
		meta, _ := params.Meta.(map[string]any)
		return meta
	}

	meta := run(t, "link-auto")
	if meta["link_id"] != "link-auto" || meta["link_is_implicit"] != false {
		t.Fatalf("explicit link meta = %#v", meta)
	}
	meta = run(t, mcp.ImplicitAppLinkIDPrefix+"acct-1")
	if meta["link_id"] != mcp.ImplicitAppLinkIDPrefix+"acct-1" || meta["link_is_implicit"] != true {
		t.Fatalf("implicit link meta = %#v", meta)
	}
	meta = run(t, "")
	if _, ok := meta["link_id"]; ok {
		t.Fatalf("no-link meta must omit link_id: %#v", meta)
	}
	if _, ok := meta["link_is_implicit"]; ok {
		t.Fatalf("no-link meta must omit link_is_implicit: %#v", meta)
	}
}
