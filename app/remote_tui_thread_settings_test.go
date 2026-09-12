package app

import (
	"encoding/json"
	"testing"

	bubbletea "github.com/charmbracelet/bubbletea"

	"codex_go/appserver"
	codextui "codex_go/tui"
	codextea "codex_go/tui/tea"
)

// TestRemoteTUIRoutesThreadSettingsUpdated covers Rust #43330/#43340: the
// app-server settings notification reaches the TUI for the active thread and is
// dropped for other threads.
func TestRemoteTUIRoutesThreadSettingsUpdated(t *testing.T) {
	state := codextui.NewState(nil)
	state.SetThreadID("thread-a")
	messages := make(chan bubbletea.Msg, 4)
	client := &remoteAppServerTUIClient{state: state, messages: messages}

	params, err := json.Marshal(appserver.SettingsUpdatedNotification{
		ThreadID:       "thread-a",
		ThreadSettings: appserver.Settings{Model: "server-model", ApprovalPolicy: "on-request", SandboxPolicy: "workspace-write"},
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := client.handleNotification(remoteAppServerMessage{
		Method: string(appserver.NotificationThreadSettingsUpdated),
		Params: params,
	}); err != nil {
		t.Fatalf("handleNotification: %v", err)
	}
	select {
	case msg := <-messages:
		updated, ok := msg.(codextea.ThreadSettingsUpdatedMsg)
		if !ok {
			t.Fatalf("message = %T, want ThreadSettingsUpdatedMsg", msg)
		}
		if updated.ThreadID != "thread-a" || updated.Settings.Model != "server-model" {
			t.Fatalf("settings message = %#v", updated)
		}
	default:
		t.Fatal("active-thread settings notification was dropped")
	}

	otherParams, err := json.Marshal(appserver.SettingsUpdatedNotification{ThreadID: "thread-b"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := client.handleNotification(remoteAppServerMessage{
		Method: string(appserver.NotificationThreadSettingsUpdated),
		Params: otherParams,
	}); err != nil {
		t.Fatalf("handleNotification: %v", err)
	}
	select {
	case msg := <-messages:
		t.Fatalf("inactive-thread settings notification forwarded: %#v", msg)
	default:
	}
}
