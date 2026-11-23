package app

import (
	"encoding/json"
	"testing"

	bubbletea "charm.land/bubbletea/v2"

	"codex_go/appserver"
	codextui "codex_go/tui"
	codextea "codex_go/tui/tea"
)

// Rust #62858: thread/status/changed reaches the TUI as an agents-overview
// status update, including the active flags that decide the needs-you group.
func TestRemoteThreadStatusChangedReachesOverviewLikeRust(t *testing.T) {
	state := codextui.NewState(nil)
	state.SetThreadID("cloud-thread")
	messages := make(chan bubbletea.Msg, 1)
	client := &remoteAppServerTUIClient{state: state, messages: messages}
	payload, err := json.Marshal(map[string]any{
		"threadId": "cloud-thread",
		"status": map[string]any{
			"type":        "active",
			"activeFlags": []string{"waitingOnUserInput"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := client.handleNotification(remoteAppServerMessage{
		Method: string(appserver.NotificationThreadStatusChanged),
		Params: payload,
	}); err != nil {
		t.Fatal(err)
	}
	msg, ok := (<-messages).(codextea.AgentThreadStatusMsg)
	if !ok {
		t.Fatalf("message = %#v", msg)
	}
	if msg.ThreadID != "cloud-thread" || msg.StatusType != "active" || !msg.Active || msg.WaitingOnApproval || !msg.WaitingOnUserInput {
		t.Fatalf("status message = %#v", msg)
	}
}
