package appserver

import (
	"context"
	"fmt"
	"strings"

	"codex_go/mcp"
)

type appserverMCPProgressHandler struct {
	notify func(NotificationMethod, any)
}

func (h *appserverMCPProgressHandler) HandleMCPProgress(ctx context.Context, notification *mcp.MCPProgressNotification) {
	_ = ctx
	if h == nil || h.notify == nil || notification == nil {
		return
	}
	threadID := strings.TrimSpace(notification.ThreadID)
	turnID := strings.TrimSpace(notification.TurnID)
	itemID := strings.TrimSpace(notification.ItemID)
	if threadID == "" || turnID == "" || itemID == "" {
		return
	}
	h.notify(NotificationMCPToolCallProgress, &MCPToolCallProgressNotification{
		ThreadID:      threadID,
		TurnID:        turnID,
		ItemID:        itemID,
		ServerName:    strings.TrimSpace(notification.ServerName),
		ProgressToken: notification.ProgressToken,
		Progress:      cloneFloat64Ptr(notification.Progress),
		Total:         cloneFloat64Ptr(notification.Total),
		Message:       mcpProgressMessage(notification),
		Params:        cloneAnyMap(notification.Params),
	})
}

func mcpProgressMessage(notification *mcp.MCPProgressNotification) string {
	if notification == nil {
		return ""
	}
	if message := strings.TrimSpace(notification.Message); message != "" {
		return message
	}
	if notification.Progress != nil && notification.Total != nil {
		return fmt.Sprintf("Progress %.0f/%.0f", *notification.Progress, *notification.Total)
	}
	if notification.Progress != nil {
		return fmt.Sprintf("Progress %.0f", *notification.Progress)
	}
	return "MCP tool call is in progress"
}
