package appserver

import (
	"context"
	"strings"

	"codex_go/mcp"
)

type appserverMCPOAuthLoginCompletionHandler struct {
	notify func(NotificationMethod, any)
	// invalidateRuntimes mirrors Rust's post-login
	// thread_manager.invalidate_mcp_runtimes(): a successful login changes the
	// servers' credentials, so the loaded threads rebuild their MCP runtimes.
	invalidateRuntimes func()
}

func (h *appserverMCPOAuthLoginCompletionHandler) HandleMCPOAuthLoginCompleted(ctx context.Context, completion *mcp.MCPOAuthLoginCompletion) {
	_ = ctx
	if h == nil || h.notify == nil || completion == nil {
		return
	}
	name := strings.TrimSpace(completion.Name)
	if name == "" {
		return
	}
	var threadID *string
	if value := strings.TrimSpace(completion.ThreadID); value != "" {
		threadID = &value
	}
	var errText *string
	if value := strings.TrimSpace(completion.Error); value != "" {
		errText = &value
	}
	// Rust always writes `loginId` (McpServerOauthLoginCompletedNotification has
	// no `skip_serializing_if` on it, app-server-protocol/src/protocol/v2/mcp.rs:341-348):
	// the key is present and carries the attempt id, or `null` when the MCP
	// layer did not provide one (`mcp.MCPOAuthLoginCompletion.LoginID` is empty
	// for that case — Rust's `Option::None`).
	loginID := OptionalString{Set: true}
	if value := strings.TrimSpace(completion.LoginID); value != "" {
		loginID.Value = &value
	}
	if completion.Success && h.invalidateRuntimes != nil {
		h.invalidateRuntimes()
	}
	h.notify(NotificationMCPServerOauthLoginCompleted, &MCPServerOauthLoginCompletedNotification{
		Name:     name,
		ThreadID: threadID,
		LoginID:  loginID,
		Success:  completion.Success,
		Error:    errText,
	})
}
