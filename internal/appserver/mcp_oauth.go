package appserver

import (
	"context"
	"strings"

	"codex_go/internal/mcp"
)

type appserverMCPOAuthLoginCompletionHandler struct {
	notify func(NotificationMethod, any)
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
	h.notify(NotificationMCPServerOauthLoginCompleted, &MCPServerOauthLoginCompletedNotification{
		Name:     name,
		ThreadID: threadID,
		Success:  completion.Success,
		Error:    errText,
	})
}
