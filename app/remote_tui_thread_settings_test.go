package app

import (
	"encoding/json"
	"testing"

	bubbletea "github.com/charmbracelet/bubbletea"

	"codex_go/appserver"
	codextui "codex_go/tui"
	codextea "codex_go/tui/tea"
)

// TestRemoteTUIRoutesThreadSettingsUpdated covers Rust #43330/#43340/#44957: the
// app-server settings notification applies to the active thread, while another
// thread's update is forwarded thread-scoped so the agents dashboard can patch
// its retained row.
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

	otherParams, err := json.Marshal(appserver.SettingsUpdatedNotification{
		ThreadID:       "thread-b",
		ThreadSettings: appserver.Settings{Model: "background-model"},
	})
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
		scoped, ok := msg.(codextea.ThreadScopedSettingsUpdatedMsg)
		if !ok {
			t.Fatalf("message = %T, want ThreadScopedSettingsUpdatedMsg", msg)
		}
		if scoped.ThreadID != "thread-b" || scoped.Settings.Model != "background-model" {
			t.Fatalf("scoped settings message = %#v", scoped)
		}
	default:
		t.Fatal("inactive-thread settings notification was dropped")
	}
}
