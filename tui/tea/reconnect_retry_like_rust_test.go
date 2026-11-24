package tea

import (
	"errors"
	"strings"
	"testing"

	codextui "codex_go/tui"
	"codex_go/tui/agents_overview"
	"codex_go/tui/keymsg"
)

// disconnectedRetryModel builds an offline conversation with an unsent draft,
// mirroring a turn whose transport failed (Rust #63599).
func disconnectedRetryModel(t *testing.T, resume SessionResumeFunc) *Model {
	t.Helper()
	state := codextui.NewState(nil)
	state.SetThreadID("thread-a")
	state.CWD = "/workspace"
	m := NewModel(state, Options{Width: 120, Height: 40, OnResumeSession: resume})
	m.composer.SetValue("unsent draft")
	updated, _ := m.Update(AppServerDisconnectMsg{})
	model := updated.(*Model)
	if !model.appServerDisconnected || !model.reconnectFailed {
		t.Fatalf("disconnect did not enter the failed offline state: %#v", model)
	}
	return model
}

// TestCtrlRRetriesReconnectLikeRust mirrors Rust #63599: an eligible
// disconnected conversation offers the shortcut, keeps its draft, shows the
// attempt, and clears the offline state when the resume succeeds.
func TestCtrlRRetriesReconnectLikeRust(t *testing.T) {
	if got := KeySpecFromKeyMsg(keymsg.KeyMsg{Type: keymsg.KeyCtrlR}); got != "ctrl-r" {
		t.Fatalf("ctrl+r key spec = %q", got)
	}
	var selections []codextui.SessionSelection
	model := disconnectedRetryModel(t, func(selection codextui.SessionSelection) (SessionResumeResponse, error) {
		selections = append(selections, selection)
		return SessionResumeResponse{}, nil
	})
	if !model.canRetryReconnect() {
		t.Fatal("eligible conversation cannot retry")
	}
	if model.footerHintOverride != "ctrl+r reconnect \u00b7 ctrl+c quit" {
		t.Fatalf("offline footer = %q", model.footerHintOverride)
	}

	updated, cmd := model.Update(keymsg.KeyMsg{Type: keymsg.KeyCtrlR})
	model = updated.(*Model)
	if cmd == nil {
		t.Fatal("ctrl+r did not start a reconnect attempt")
	}
	if model.composer.Value() != "unsent draft" {
		t.Fatalf("draft changed during retry: %q", model.composer.Value())
	}
	if model.reconnectAttemptThreadID != "thread-a" || !model.retryActivityActive {
		t.Fatalf("attempt not marked running: %#v", model)
	}
	if model.footerHintOverride != "ctrl+c quit" {
		t.Fatalf("running attempt footer = %q", model.footerHintOverride)
	}
	if model.notice != "Reconnecting to server\u2026" {
		t.Fatalf("running attempt notice = %q", model.notice)
	}

	updated, _ = model.Update(cmd())
	model = updated.(*Model)
	if len(selections) != 1 {
		t.Fatalf("resume calls = %d, want 1", len(selections))
	}
	if selections[0].Target.ThreadID != "thread-a" || selections[0].Target.CWD != "/workspace" {
		t.Fatalf("resume selection = %#v", selections[0])
	}
	if model.appServerDisconnected || model.statuslineDisconnected || model.reconnectFailed {
		t.Fatalf("successful retry kept the offline state: disconnected=%v statusline=%v failed=%v",
			model.appServerDisconnected, model.statuslineDisconnected, model.reconnectFailed)
	}
	if model.footerHintOverride != "" {
		t.Fatalf("successful retry kept footer override %q", model.footerHintOverride)
	}
	if model.retryActivityActive {
		t.Fatal("successful retry kept the reconnect row")
	}
	if model.notice != "Reconnected to the server." {
		t.Fatalf("success notice = %q", model.notice)
	}
}

// TestCtrlRRetryFailureKeepsRetryHintLikeRust mirrors Rust #63599: a failed
// attempt leaves the conversation offline, restores the retry hint, and can be
// pressed again.
func TestCtrlRRetryFailureKeepsRetryHintLikeRust(t *testing.T) {
	model := disconnectedRetryModel(t, func(codextui.SessionSelection) (SessionResumeResponse, error) {
		return SessionResumeResponse{}, errors.New("server still unreachable")
	})
	updated, cmd := model.Update(keymsg.KeyMsg{Type: keymsg.KeyCtrlR})
	model = updated.(*Model)
	if cmd == nil {
		t.Fatal("ctrl+r did not start a reconnect attempt")
	}
	updated, _ = model.Update(cmd())
	model = updated.(*Model)
	if !model.appServerDisconnected || !model.reconnectFailed {
		t.Fatalf("failed retry cleared the offline state: %#v", model)
	}
	if !strings.Contains(model.notice, "Reconnect failed") || !strings.Contains(model.notice, "Ctrl+R") {
		t.Fatalf("failure notice = %q", model.notice)
	}
	if model.footerHintOverride != "ctrl+r reconnect \u00b7 ctrl+c quit" {
		t.Fatalf("failure footer = %q", model.footerHintOverride)
	}
	if model.retryActivityActive {
		t.Fatal("failure kept the running attempt row")
	}
	if model.composer.Value() != "unsent draft" {
		t.Fatalf("draft changed after failure: %q", model.composer.Value())
	}
	if !model.canRetryReconnect() {
		t.Fatal("a failed attempt cannot be retried")
	}
}

// TestCtrlRRetryRespectsVimNormalModeLikeRust mirrors Rust #63599: Vim Normal
// mode keeps Ctrl+R redo, while Insert mode accepts the reconnect shortcut.
func TestCtrlRRetryRespectsVimNormalModeLikeRust(t *testing.T) {
	model := disconnectedRetryModel(t, func(codextui.SessionSelection) (SessionResumeResponse, error) {
		return SessionResumeResponse{}, nil
	})
	model.vimMode = true
	model.vimInsert = false

	updated, _ := model.Update(keymsg.KeyMsg{Type: keymsg.KeyCtrlR})
	model = updated.(*Model)
	if model.reconnectAttemptThreadID != "" {
		t.Fatal("Vim Normal mode started a reconnect attempt")
	}

	model.vimInsert = true
	updated, cmd := model.Update(keymsg.KeyMsg{Type: keymsg.KeyCtrlR})
	model = updated.(*Model)
	if cmd == nil || model.reconnectAttemptThreadID != "thread-a" {
		t.Fatal("Vim Insert mode did not start the reconnect attempt")
	}
}

// TestCloudConversationDoesNotOfferCtrlRRetryLikeRust mirrors Rust #63599:
// Cloud sessions reconnect through their own stream and have no Ctrl+R retry.
func TestCloudConversationDoesNotOfferCtrlRRetryLikeRust(t *testing.T) {
	model := disconnectedRetryModel(t, func(codextui.SessionSelection) (SessionResumeResponse, error) {
		return SessionResumeResponse{}, nil
	})
	model.serverOwnedSettings = true
	model.reconnectFailed = true
	if model.canRetryReconnect() {
		t.Fatal("Cloud conversation offered a Ctrl+R retry")
	}
	updated, _ := model.Update(keymsg.KeyMsg{Type: keymsg.KeyCtrlR})
	model = updated.(*Model)
	if model.reconnectAttemptThreadID != "" {
		t.Fatal("Cloud conversation started a reconnect attempt")
	}
}

// TestOfflineAgentsOverviewPointsBackToCtrlRLikeRust mirrors Rust #63599:
// the offline overview tells the user to return to the conversation and
// retry, instead of suggesting a relaunch.
func TestOfflineAgentsOverviewPointsBackToCtrlRLikeRust(t *testing.T) {
	model := disconnectedRetryModel(t, func(codextui.SessionSelection) (SessionResumeResponse, error) {
		return SessionResumeResponse{}, nil
	})
	model.onAgentsOverviewRefresh = func(string) ([]agentsoverview.Row, error) { return nil, nil }
	_ = model.applyAgentsCommand()
	want := "Reconnect failed \u2014 return to the conversation and press Ctrl+R to retry"
	if model.agentsOverviewNotice != want {
		t.Fatalf("offline overview notice = %q, want %q", model.agentsOverviewNotice, want)
	}
}
