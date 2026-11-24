package app

import (
	"encoding/json"
	"testing"

	bubbletea "charm.land/bubbletea/v2"

	"codex_go/appserver"
	codextui "codex_go/tui"
	"codex_go/tui/chatwidget"
	codextea "codex_go/tui/tea"
)

// Rust #59767: a side thread's MCP startup statuses can arrive before the
// connection displays that thread. Park them instead of dropping them, then
// replay once the thread is active.
func TestDeferredMCPStartupStatusReplaysForSideThreadLikeRust(t *testing.T) {
	state := codextui.NewState(nil)
	state.SetThreadID("thread-parent")
	messages := make(chan bubbletea.Msg, 8)
	client := &remoteAppServerTUIClient{state: state, messages: messages}

	failure := "startup failed"
	threadID := "thread-side"
	payload, err := json.Marshal(appserver.MCPServerStatusUpdatedNotification{
		ThreadID: &threadID,
		Name:     "tools",
		Status:   string(chatwidget.McpStartupFailed),
		Error:    &failure,
	})
	if err != nil {
		t.Fatalf("marshal notification: %v", err)
	}
	if err := client.handleNotification(remoteAppServerMessage{
		Method: string(appserver.NotificationMcpServerStatusUpdated),
		Params: payload,
	}); err != nil {
		t.Fatalf("handleNotification: %v", err)
	}
	select {
	case msg := <-messages:
		t.Fatalf("status reached the parent thread: %#v", msg)
	default:
	}

	// The TUI switches to the side thread while the fork result is applied.
	state.SetThreadID(threadID)
	if err := client.handleNotification(remoteAppServerMessage{
		Method: string(appserver.NotificationMcpServerStatusUpdated),
		Params: mustMarshalDeferredMCPStatus(t, "thread-side", "other", string(chatwidget.McpStartupStarting)),
	}); err != nil {
		t.Fatalf("handleNotification: %v", err)
	}
	replayed, ok := (<-messages).(codextea.MCPStartupUpdateMsg)
	if !ok || replayed.Name != "tools" || replayed.Status.Kind != chatwidget.McpStartupFailed || replayed.Status.Error != "startup failed" {
		t.Fatalf("replayed status = %#v", replayed)
	}
	live, ok := (<-messages).(codextea.MCPStartupUpdateMsg)
	if !ok || live.Name != "other" || live.Status.Kind != chatwidget.McpStartupStarting {
		t.Fatalf("live status = %#v", live)
	}
}

func mustMarshalDeferredMCPStatus(t *testing.T, threadID, name, status string) json.RawMessage {
	t.Helper()
	payload, err := json.Marshal(appserver.MCPServerStatusUpdatedNotification{
		ThreadID: &threadID,
		Name:     name,
		Status:   status,
	})
	if err != nil {
		t.Fatalf("marshal notification: %v", err)
	}
	return payload
}
