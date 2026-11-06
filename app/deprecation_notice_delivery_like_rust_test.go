package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	bubbletea "charm.land/bubbletea/v2"

	"codex_go/appserver"
	"codex_go/cli"
	"codex_go/protocol"
	codextui "codex_go/tui"
	codextea "codex_go/tui/tea"
	"codex_go/utils"
)

// Rust #47065: the app-server's deprecationNotice notification reaches the TUI
// through the remote client, is rendered once, and is deduplicated against the
// notice already retained in the transcript.
func TestRemoteDeprecationNoticeDeliveryDedupesLikeRust(t *testing.T) {
	state := codextui.NewState(nil)
	state.SetThreadID("thread-a")
	messages := make(chan bubbletea.Msg, 4)
	client := &remoteAppServerTUIClient{state: state, messages: messages}
	details := "Use thread/turns/list and thread/items/list."
	payload, err := json.Marshal(appserver.DeprecationNoticeNotification{
		Summary: "Full-history hydration is deprecated for paginated threads",
		Details: &details,
	})
	if err != nil {
		t.Fatalf("marshal notification: %v", err)
	}
	model := codextea.NewModel(state, codextea.Options{Width: 100, Height: 30})
	deliver := func() {
		t.Helper()
		if err := client.handleNotification(remoteAppServerMessage{
			Method: string(appserver.NotificationDeprecationNotice),
			Params: payload,
		}); err != nil {
			t.Fatalf("handleNotification: %v", err)
		}
		select {
		case message := <-messages:
			event, ok := message.(codextea.ThreadEventMsg)
			if !ok {
				t.Fatalf("message = %#v, want thread event", message)
			}
			if event.Event.Type != "deprecation.notice" {
				t.Fatalf("event type = %q, want deprecation.notice", event.Event.Type)
			}
			model.Update(message)
		default:
			t.Fatal("the deprecation notice was dropped by the remote client")
		}
	}
	deliver()
	deliver()

	notices := 0
	for _, message := range model.State.Messages {
		if message.DeprecationNotice != nil {
			notices++
		}
	}
	if notices != 1 {
		t.Fatalf("deprecation notices = %d, want 1", notices)
	}
	view := utils.StripANSI(model.ViewContent())
	if !strings.Contains(view, "Full-history hydration is deprecated for paginated threads") {
		t.Fatalf("notice missing from the rendered view:\n%s", view)
	}
	if !strings.Contains(view, "Use thread/turns/list and thread/items/list.") {
		t.Fatalf("notice details missing from the rendered view:\n%s", view)
	}
}

// Rust #47065: a rebuilt transcript (thread switch) is a fresh deduplication
// scope, so the same server notice can be shown again.
func TestRemoteDeprecationNoticeShowsAgainAfterThreadRebuildLikeRust(t *testing.T) {
	state := codextui.NewState(nil)
	state.SetThreadID("thread-a")
	messages := make(chan bubbletea.Msg, 2)
	client := &remoteAppServerTUIClient{state: state, messages: messages}
	payload, err := json.Marshal(appserver.DeprecationNoticeNotification{
		Summary: "Old flag is deprecated",
	})
	if err != nil {
		t.Fatalf("marshal notification: %v", err)
	}
	model := codextea.NewModel(state, codextea.Options{Width: 100, Height: 30})
	deliver := func() {
		t.Helper()
		if err := client.handleNotification(remoteAppServerMessage{
			Method: string(appserver.NotificationDeprecationNotice),
			Params: payload,
		}); err != nil {
			t.Fatalf("handleNotification: %v", err)
		}
		model.Update(<-messages)
	}
	deliver()
	// Switching threads replaces the retained transcript before the new
	// thread's history is replayed.
	if _, cmd := model.Update(codextea.ThreadEventMsg{Event: protocol.ThreadStarted("thread-b")}); cmd != nil {
		// The command is a batch of background reads; the test only needs the
		// synchronous state transition.
	}
	state.Messages = nil
	state.SetThreadID("thread-b")
	deliver()
	notices := 0
	for _, message := range state.Messages {
		if message.DeprecationNotice != nil {
			notices++
		}
	}
	if notices != 1 {
		t.Fatalf("deprecation notices after the thread switch = %d, want 1", notices)
	}
}

// Rust #47065 (core session.rs legacy_feature_usages): the local session turns
// deprecated configuration features into deprecation notices that the TUI
// inserts exactly once.
func TestLocalStartupDeprecationMessagesReachTUIOnceLikeRust(t *testing.T) {
	home := t.TempDir()
	configPath := filepath.Join(home, "config.toml")
	if err := os.WriteFile(configPath, []byte("[features]\nweb_search_request = true\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	messages := interactiveStartupDeprecationMessages(&cli.RootOptions{}, home, home)
	if len(messages) == 0 {
		t.Fatal("the deprecated feature did not produce a deprecation notice")
	}
	model := codextea.NewModel(codextui.NewState(nil), codextea.Options{Width: 100, Height: 30})
	for _, message := range messages {
		model.Update(message)
	}
	notices := 0
	for _, message := range model.State.Messages {
		if message.DeprecationNotice != nil {
			notices++
		}
	}
	if notices != len(messages) {
		t.Fatalf("deprecation notices = %d, want %d", notices, len(messages))
	}
	view := utils.StripANSI(model.ViewContent())
	if !strings.Contains(view, "web_search_request") {
		t.Fatalf("the deprecated feature is missing from the rendered view:\n%s", view)
	}
}

// Rust #47065: the startup deprecation notices are delivered ahead of the MCP
// startup stream through the same channel the TUI consumes.
func TestInteractiveStartupMessagesPrependDeprecationsLikeRust(t *testing.T) {
	stream := make(chan bubbletea.Msg, 1)
	stream <- codextea.MCPStartupFinishAfterLagMsg{}
	close(stream)
	prefix := []bubbletea.Msg{codextea.ThreadEventMsg{Event: protocol.DeprecationNotice("a", "b")}}
	merged := interactiveStartupMessages(nil, stream, prefix)
	first, ok := (<-merged).(codextea.ThreadEventMsg)
	if !ok || first.Event.Type != "deprecation.notice" {
		t.Fatalf("first message = %#v, want the deprecation notice", first)
	}
	if _, ok := (<-merged).(codextea.MCPStartupFinishAfterLagMsg); !ok {
		t.Fatal("the MCP startup stream was not forwarded")
	}
	if _, open := <-merged; open {
		t.Fatal("the merged stream stayed open after both sources finished")
	}
}
