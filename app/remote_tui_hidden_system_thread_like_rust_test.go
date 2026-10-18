package app

import (
	"encoding/json"
	"testing"
	"time"

	bubbletea "github.com/charmbracelet/bubbletea"

	"codex_go/appserver"
	codextui "codex_go/tui"
	codextea "codex_go/tui/tea"
)

func TestRemoteTUIHidesEphemeralSystemThreadStartedLikeRust(t *testing.T) {
	system := appserver.ThreadSource("system")
	threadTitle := appserver.ThreadSource("thread_title")
	subagent := appserver.ThreadSourceSubagent

	tests := []struct {
		name         string
		ephemeral    bool
		threadSource *appserver.ThreadSource
		wantRouted   bool
	}{
		{name: "ephemeral system helper", ephemeral: true, threadSource: &system},
		{name: "ephemeral title worker", ephemeral: true, threadSource: &threadTitle},
		{name: "persisted system thread", threadSource: &system, wantRouted: true},
		{name: "ephemeral subagent", ephemeral: true, threadSource: &subagent, wantRouted: true},
		{name: "thread without source", wantRouted: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			messages := make(chan bubbletea.Msg, 1)
			state := codextui.NewState(nil)
			client := &remoteAppServerTUIClient{state: state, messages: messages}
			params, err := json.Marshal(appserver.ThreadStartedNotification{
				Thread: &appserver.Thread{
					ID:           "thread-started",
					Ephemeral:    tc.ephemeral,
					ThreadSource: tc.threadSource,
				},
			})
			if err != nil {
				t.Fatalf("marshal thread/started: %v", err)
			}
			if err := client.handleNotification(remoteAppServerMessage{
				Method: string(appserver.NotificationThreadStarted),
				Params: params,
			}); err != nil {
				t.Fatalf("handleNotification: %v", err)
			}

			if !tc.wantRouted {
				select {
				case message := <-messages:
					t.Fatalf("hidden thread emitted %T: %#v", message, message)
				default:
				}
				if state.ThreadID != "" {
					t.Fatalf("hidden thread changed active id to %q", state.ThreadID)
				}
				return
			}

			select {
			case message := <-messages:
				event, ok := message.(codextea.ThreadEventMsg)
				if !ok {
					t.Fatalf("message = %T, want ThreadEventMsg", message)
				}
				if event.Event.Type != "thread.started" || event.Event.ThreadID != "thread-started" {
					t.Fatalf("thread event = %#v", event.Event)
				}
				if state.ThreadID != "thread-started" {
					t.Fatalf("active thread id = %q, want thread-started", state.ThreadID)
				}
			case <-time.After(time.Second):
				t.Fatal("visible thread/started notification was not routed")
			}
		})
	}
}
