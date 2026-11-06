package tea

import (
	"strings"
	"testing"

	codextui "codex_go/tui"
)

// Rust #47338 moves the "Inspect activity" hint into the keyboard shortcut
// help, rendering the runtime global.focus_activity binding and hiding the row
// when the action is unbound.
func TestShortcutOverlayRendersRuntimeActivityHintLikeRust(t *testing.T) {
	model := NewModel(codextui.NewState(nil), Options{Width: 140, Height: 40})
	if got := shortcutOverlayKeyFor(model, "Inspect activity"); got != "f4" {
		t.Fatalf("default activity hint = %q, want f4", got)
	}

	if err := model.keymapConfig.Set("global", "focus_activity", []string{"f7"}); err != nil {
		t.Fatalf("set focus_activity: %v", err)
	}
	if got := shortcutOverlayKeyFor(model, "Inspect activity"); got != "f7" {
		t.Fatalf("remapped activity hint = %q, want f7", got)
	}

	if err := model.keymapConfig.Set("global", "focus_activity", nil); err != nil {
		t.Fatalf("unbind focus_activity: %v", err)
	}
	if got := shortcutOverlayKeyFor(model, "Inspect activity"); got != "" {
		t.Fatalf("unbound activity hint = %q, want hidden", got)
	}
	if view := plainView(model.viewString()); strings.Contains(view, "Inspect activity") {
		t.Fatalf("unbound activity hint still painted:\n%s", view)
	}
}

// Rust renders the voice row from hints.toggle_voice, so a custom binding or an
// unbound chat.toggle_voice follows the same rule.
func TestShortcutOverlayRendersRuntimeVoiceHintLikeRust(t *testing.T) {
	model := NewModel(codextui.NewState(nil), Options{Width: 140, Height: 40})
	if got := shortcutOverlayKeyFor(model, "Voice"); got != "f8" {
		t.Fatalf("default voice hint = %q, want f8", got)
	}
	if err := model.keymapConfig.Set("chat", "toggle_voice", []string{"ctrl-v"}); err != nil {
		t.Fatalf("set toggle_voice: %v", err)
	}
	if got := shortcutOverlayKeyFor(model, "Voice"); got != "ctrl+v" {
		t.Fatalf("remapped voice hint = %q, want ctrl+v", got)
	}
	if err := model.keymapConfig.Set("chat", "toggle_voice", nil); err != nil {
		t.Fatalf("unbind toggle_voice: %v", err)
	}
	if got := shortcutOverlayKeyFor(model, "Voice"); got != "" {
		t.Fatalf("unbound voice hint = %q, want hidden", got)
	}
}

// shortcutOverlayKeyFor returns the rendered key for one shortcut-help action.
func shortcutOverlayKeyFor(model *Model, action string) string {
	groups := model.shortcutOverlayGroups()
	for _, group := range groups {
		for _, entry := range group.Entries {
			if entry.Action == action {
				return entry.Key
			}
		}
	}
	return ""
}
