package app

import (
	"encoding/json"
	"testing"

	bubbletea "charm.land/bubbletea/v2"

	"codex_go/appserver"
	codextui "codex_go/tui"
	chatwidget "codex_go/tui/chatwidget"
	codextea "codex_go/tui/tea"
)

func remoteMCPStatusNotification(t *testing.T, threadID, name, status string, startupErr *string, reason *appserver.MCPServerStartupFailureReason) remoteAppServerMessage {
	t.Helper()
	params, err := json.Marshal(appserver.MCPServerStatusUpdatedNotification{
		ThreadID:      &threadID,
		Name:          name,
		Status:        status,
		Error:         startupErr,
		FailureReason: reason,
	})
	if err != nil {
		t.Fatalf("marshal mcp status: %v", err)
	}
	return remoteAppServerMessage{Method: string(appserver.NotificationMcpServerStatusUpdated), Params: params}
}

func TestRemoteMCPStartupNotificationForwardsReportedServerLikeRust(t *testing.T) {
	// Rust #60280: a client attached to a shared app server receives startup
	// updates for that server's configuration, so the notification must reach
	// the tui's reported-only startup tracking.
	threadID := "thread-1"
	messages := make(chan bubbletea.Msg, 4)
	state := codextui.NewState(nil)
	state.ThreadID = threadID
	client := &remoteAppServerTUIClient{messages: messages, state: state}

	failure := "Session expired"
	reason := appserver.MCPServerStartupFailureReauthenticationRequired
	if err := client.handleNotification(remoteMCPStatusNotification(t, threadID, "deferred", "failed", &failure, &reason)); err != nil {
		t.Fatalf("handleNotification: %v", err)
	}
	message, ok := (<-messages).(codextea.MCPStartupUpdateMsg)
	if !ok {
		t.Fatalf("expected MCPStartupUpdateMsg, got %T", message)
	}
	if message.Name != "deferred" || message.Status.Kind != chatwidget.McpStartupFailed || message.Status.Error != failure || message.Status.FailureReason != string(reason) {
		t.Fatalf("mcp status update = %#v", message)
	}
}

func TestRemoteMCPStartupNotificationIgnoresBackgroundThreadLikeRust(t *testing.T) {
	messages := make(chan bubbletea.Msg, 1)
	state := codextui.NewState(nil)
	state.ThreadID = "active-thread"
	client := &remoteAppServerTUIClient{messages: messages, state: state}

	if err := client.handleNotification(remoteMCPStatusNotification(t, "background-thread", "deferred", "starting", nil, nil)); err != nil {
		t.Fatalf("handleNotification: %v", err)
	}
	select {
	case message := <-messages:
		t.Fatalf("background thread startup update leaked into the displayed thread: %T %#v", message, message)
	default:
	}
}
