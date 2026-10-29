package tea

import (
	"strings"
	"testing"

	bubbletea "github.com/charmbracelet/bubbletea"

	codextui "codex_go/tui"
)

func newQueuedMessageHintModel(t *testing.T, config *codextui.KeymapConfig) *Model {
	t.Helper()
	model := NewModel(codextui.NewState(nil), Options{Width: 120, Height: 36, KeymapConfig: config})
	model.composer.SetValue("queued follow-up")
	model.queueComposer(false)
	if len(model.queued) != 1 {
		t.Fatalf("seeded queue = %d, want 1", len(model.queued))
	}
	return model
}

// TestQueuedMessageEditHintUsesPrimaryShortcutLikeRust mirrors Rust #47618:
// the queued-message hint shows the first configured edit_queued_message
// shortcut, including custom chords, and disappears when the action is
// unbound instead of falling back to Alt+Up.
func TestQueuedMessageEditHintUsesPrimaryShortcutLikeRust(t *testing.T) {
	tests := []struct {
		name     string
		bindings []string
		want     string
	}{
		{name: "default", bindings: nil, want: "shift+← edit last queued message"},
		{name: "configured order", bindings: []string{"alt-up", "shift-left"}, want: "alt+↑ edit last queued message"},
		{name: "custom chord", bindings: []string{"ctrl-x up", "shift-left"}, want: "ctrl+x ↑ edit last queued message"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			config := codextui.NewKeymapConfig()
			if tc.bindings != nil {
				if err := config.Set("chat", "edit_queued_message", tc.bindings); err != nil {
					t.Fatalf("set edit_queued_message: %v", err)
				}
			}
			model := newQueuedMessageHintModel(t, config)
			if view := model.View(); !strings.Contains(view, tc.want) {
				t.Fatalf("queued hint missing %q:\n%s", tc.want, view)
			}
		})
	}

	config := codextui.NewKeymapConfig()
	if err := config.Set("chat", "edit_queued_message", []string{}); err != nil {
		t.Fatalf("unbind edit_queued_message: %v", err)
	}
	model := newQueuedMessageHintModel(t, config)
	if view := model.View(); strings.Contains(view, "edit last queued message") {
		t.Fatalf("unbound edit hint must be hidden:\n%s", view)
	}
	for _, key := range []bubbletea.KeyMsg{
		{Type: bubbletea.KeyShiftLeft},
		{Type: bubbletea.KeyUp, Alt: true},
	} {
		updated, _ := model.Update(key)
		model = updated.(*Model)
		if model.composer.Value() != "" || len(model.queued) != 1 {
			t.Fatalf("unbound %v restored a queued message: composer=%q queue=%d", key, model.composer.Value(), len(model.queued))
		}
	}
}

// TestQueuedMessageEditAcceptsBothDefaultShortcutsLikeRust mirrors Rust
// default_shortcuts_edit_most_recent_queued_message: Shift+Left is primary,
// and Alt+Up remains an accepted alternative.
func TestQueuedMessageEditAcceptsBothDefaultShortcutsLikeRust(t *testing.T) {
	for _, key := range []bubbletea.KeyMsg{
		{Type: bubbletea.KeyShiftLeft},
		{Type: bubbletea.KeyUp, Alt: true},
	} {
		model := NewModel(codextui.NewState(nil), Options{Width: 120, Height: 36, KeymapConfig: codextui.NewKeymapConfig()})
		model.composer.SetValue("first queued")
		model.queueComposer(false)
		model.composer.SetValue("second queued")
		model.queueComposer(false)

		updated, _ := model.Update(key)
		model = updated.(*Model)
		if got := model.composer.Value(); got != "second queued" {
			t.Fatalf("%v composer = %q, want the most recent queued message", key, got)
		}
		if len(model.queued) != 1 || model.queued[0].Request.Prompt != "first queued" {
			t.Fatalf("%v queue = %#v, want only the older message", key, model.queued)
		}
	}
}
