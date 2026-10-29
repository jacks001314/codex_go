package app

import (
	"testing"

	bubbletea "github.com/charmbracelet/bubbletea"

	codextea "codex_go/tui/tea"
)

// TestRemoteClosingThreadErrorDoesNotTriggerReconnectLikeRust pins the
// architectural difference behind Rust #42207. Go's TUI has no persistent
// app-server reconnect loop (#41916/#41918 are not applicable), so a
// thread/resume "is closing" response is a server RPC error surfaced to the
// caller, not a transport disconnect that enters the voice-only
// AppServerDisconnect path.
func TestRemoteClosingThreadErrorDoesNotTriggerReconnectLikeRust(t *testing.T) {
	err := &remoteRPCError{
		Code:    -32600,
		Message: "thread thread-1 is closing; retry after the thread is closed",
	}
	if isRemoteTransportError(err) {
		t.Fatal("closing-thread RPC error must not be classified as a transport failure")
	}
	messages := make(chan bubbletea.Msg, 4)
	sendRemoteTurnError(messages, err)
	close(messages)

	sawThreadError := false
	sawTurnCompleted := false
	for message := range messages {
		switch typed := message.(type) {
		case codextea.AppServerDisconnectMsg:
			t.Fatal("closing-thread RPC error entered the reconnect/disconnect path")
		case codextea.ThreadEventMsg:
			if typed.Event.Type == "error" && typed.Event.Error != nil {
				sawThreadError = true
			}
		case codextea.TurnCompletedMsg:
			if typed.Err != nil {
				sawTurnCompleted = true
			}
		}
	}
	if !sawThreadError || !sawTurnCompleted {
		t.Fatalf("closing-thread error was not surfaced: threadError=%v completed=%v", sawThreadError, sawTurnCompleted)
	}
}
