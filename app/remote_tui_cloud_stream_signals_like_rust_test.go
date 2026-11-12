package app

import (
	"encoding/json"
	"testing"

	bubbletea "charm.land/bubbletea/v2"

	"codex_go/cloudbackend"
	codextui "codex_go/tui"
	codextea "codex_go/tui/tea"
)

// Mirrors Rust #60969: the Cloud transport's internal stream signals reach the
// TUI as hold/release messages for the displayed conversation.
func TestRemoteCloudStreamSignalsHoldInputLikeRust(t *testing.T) {
	state := codextui.NewState(nil)
	state.SetThreadID("cloud-thread")
	messages := make(chan bubbletea.Msg, 2)
	client := &remoteAppServerTUIClient{state: state, messages: messages}
	payload, err := json.Marshal(map[string]string{"threadId": "cloud-thread"})
	if err != nil {
		t.Fatal(err)
	}
	if err := client.handleNotification(remoteAppServerMessage{
		Method: string(cloudbackend.NotificationCloudStreamDisconnected), Params: payload,
	}); err != nil {
		t.Fatal(err)
	}
	if msg, ok := (<-messages).(codextea.CloudStreamDisconnectedMsg); !ok || msg.ThreadID != "cloud-thread" {
		t.Fatalf("disconnect message = %#v", msg)
	}
	if err := client.handleNotification(remoteAppServerMessage{
		Method: string(cloudbackend.NotificationCloudStreamReconnected), Params: payload,
	}); err != nil {
		t.Fatal(err)
	}
	if msg, ok := (<-messages).(codextea.CloudStreamReconnectedMsg); !ok || msg.ThreadID != "cloud-thread" {
		t.Fatalf("reconnect message = %#v", msg)
	}
}
