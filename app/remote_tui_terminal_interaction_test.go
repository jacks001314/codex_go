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

// TestRemoteTUIRoutesTerminalInteraction covers Rust #43921: a terminal
// interaction for the active thread reaches the TUI so it can track the wait
// streak, while another thread's interaction is ignored.
func TestRemoteTUIRoutesTerminalInteraction(t *testing.T) {
	messages := make(chan bubbletea.Msg, 1)
	state := codextui.NewState(nil)
	state.SetThreadID("thread-main")
	client := &remoteAppServerTUIClient{state: state, messages: messages}

	other, err := json.Marshal(appserver.TerminalInteractionNotification{ThreadID: "thread-other", ProcessID: "proc-other"})
	if err != nil {
		t.Fatalf("marshal other notification: %v", err)
	}
	if err := client.handleNotification(remoteAppServerMessage{
		Method: string(appserver.NotificationTerminalInteraction),
		Params: other,
	}); err != nil {
		t.Fatalf("handle other interaction: %v", err)
	}
	select {
	case message := <-messages:
		t.Fatalf("another thread's interaction was forwarded: %T", message)
	default:
	}

	params, err := json.Marshal(appserver.TerminalInteractionNotification{
		ThreadID:  "thread-main",
		TurnID:    "turn-1",
		ItemID:    "call-1",
		ProcessID: "proc-1",
		Stdin:     "",
	})
	if err != nil {
		t.Fatalf("marshal notification: %v", err)
	}
	if err := client.handleNotification(remoteAppServerMessage{
		Method: string(appserver.NotificationTerminalInteraction),
		Params: params,
	}); err != nil {
		t.Fatalf("handle terminal interaction: %v", err)
	}
	select {
	case message := <-messages:
		interaction, ok := message.(codextea.TerminalInteractionMsg)
		if !ok {
			t.Fatalf("message = %T, want TerminalInteractionMsg", message)
		}
		if interaction.ThreadID != "thread-main" || interaction.ProcessID != "proc-1" || interaction.ItemID != "call-1" || interaction.Stdin != "" {
			t.Fatalf("interaction = %#v", interaction)
		}
	case <-time.After(time.Second):
		t.Fatal("terminal interaction was dropped")
	}
}

// TestRemoteProtocolItemCarriesProcessID covers the tracking prerequisite: the
// command-execution item keeps its source and process id for the TUI lifecycle.
func TestRemoteProtocolItemCarriesProcessID(t *testing.T) {
	item := remoteProtocolItemFromPayload(appserver.ThreadItemPayload{
		"id":        "call-1",
		"type":      "commandExecution",
		"command":   "python server.py",
		"status":    "inProgress",
		"source":    string(appserver.CommandExecutionSourceUnifiedExecStartup),
		"processId": "proc-1",
	}, false)
	if item.Metadata == nil || item.Metadata["source"] != string(appserver.CommandExecutionSourceUnifiedExecStartup) || item.Metadata["processId"] != "proc-1" {
		t.Fatalf("item metadata = %#v", item.Metadata)
	}
}

// TestRemoteProtocolItemCarriesReasoningSummary covers Rust #43921's
// reconciliation prerequisite: a completed reasoning item keeps its complete
// summary so a refreshed stream can be reconciled.
func TestRemoteProtocolItemCarriesReasoningSummary(t *testing.T) {
	item := remoteProtocolItemFromPayload(appserver.ThreadItemPayload{
		"id":      "reasoning-1",
		"type":    "reasoning",
		"summary": []any{"## Complete summary", "Details"},
		"content": []any{"raw reasoning"},
	}, true)
	if item.ID != "reasoning-1" || item.Type != "reasoning" {
		t.Fatalf("reasoning item = %#v", item)
	}
	want := "## Complete summary\nDetails\nraw reasoning"
	if item.Text != want {
		t.Fatalf("reasoning text = %q, want %q", item.Text, want)
	}
}
