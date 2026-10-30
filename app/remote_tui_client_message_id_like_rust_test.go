package app

import (
	"testing"

	"codex_go/appserver"
	"codex_go/cli"
	codextui "codex_go/tui"
	codextea "codex_go/tui/tea"
)

// TestRemoteTurnStartCarriesClientMessageIDLikeRust covers Rust #42451: the TUI
// submission id is sent on turn/start so the committed user message echoes it.
func TestRemoteTurnStartCarriesClientMessageIDLikeRust(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir())
	state := codextui.NewState(nil)
	params, err := remoteTurnStartParams(&cli.RootOptions{}, state, "thread-start", codextea.SubmitRequest{
		Prompt:   "hello",
		ClientID: "tui-submission-1",
	})
	if err != nil {
		t.Fatalf("remoteTurnStartParams() error = %v", err)
	}
	if params.ClientUserMessageID != "tui-submission-1" {
		t.Fatalf("params.ClientUserMessageID = %q", params.ClientUserMessageID)
	}
}

// TestRemoteTurnSteerParamsFallsBackToRequestClientIDLikeRust keeps the
// submission id on turn/steer even when the caller does not pass it separately.
func TestRemoteTurnSteerParamsFallsBackToRequestClientIDLikeRust(t *testing.T) {
	params, err := remoteTurnSteerParams("thread-steer", "turn-steer", "", codextea.SubmitRequest{
		Prompt:   "change direction",
		ClientID: "tui-submission-2",
	})
	if err != nil {
		t.Fatalf("remoteTurnSteerParams() error = %v", err)
	}
	if params.ClientUserMessageID != "tui-submission-2" {
		t.Fatalf("params.ClientUserMessageID = %q", params.ClientUserMessageID)
	}
}

// TestRemoteProtocolUserMessageCarriesClientIDAndImageCountLikeRust pins the
// receipt fields the pending-steer acknowledgement relies on.
func TestRemoteProtocolUserMessageCarriesClientIDAndImageCountLikeRust(t *testing.T) {
	item := remoteProtocolItemFromPayload(appserver.ThreadItemPayload{
		"type":     "userMessage",
		"id":       "item-1",
		"clientId": "tui-submission-3",
		"content": []any{
			map[string]any{"type": "text", "text": "look at these"},
			map[string]any{"type": "image", "url": "https://example.test/a.png"},
			map[string]any{"type": "localImage", "path": "/tmp/b.png"},
		},
	}, true)
	if item.ClientID != "tui-submission-3" {
		t.Fatalf("item.ClientID = %q", item.ClientID)
	}
	if item.ImageCount != 2 {
		t.Fatalf("item.ImageCount = %d, want 2", item.ImageCount)
	}
	if item.Text != "look at these" {
		t.Fatalf("item.Text = %q", item.Text)
	}
}
