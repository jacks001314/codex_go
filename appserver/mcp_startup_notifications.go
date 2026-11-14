package appserver

import (
	"strings"

	"codex_go/mcp"
)

// observeMCPStartupStatus forwards registration-scoped startup transitions to
// the mcpServer/startupStatus/updated notification (Rust #61257).
func (r *RuntimeRouter) observeMCPStartupStatus(service *mcp.MCPService, threadID string) {
	if r == nil || service == nil {
		return
	}
	thread := strings.TrimSpace(threadID)
	service.AddStartupStatusListener(func(name, environmentID string, status mcp.MCPServerStartupState, failureReason *string, err error) {
		notification := MCPServerStatusUpdatedNotification{
			Name:   strings.TrimSpace(name),
			Status: string(status),
		}
		if thread != "" {
			notification.ThreadID = &thread
		}
		if scope := strings.TrimSpace(environmentID); scope != "" && scope != mcp.DefaultMCPServerEnvironmentID {
			notification.EnvironmentID = &scope
		}
		if err != nil {
			message := strings.TrimSpace(err.Error())
			if message != "" {
				notification.Error = &message
			}
		}
		if failureReason != nil {
			if reason := strings.TrimSpace(*failureReason); reason != "" {
				value := MCPServerStartupFailureReason(reason)
				notification.FailureReason = &value
			}
		}
		r.notify(NotificationMCPServerStatusUpdated, notification)
	})
}
