package tea

import (
	"strings"
	"testing"

	bubbletea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	codextui "codex_go/tui"
)

// Rust's footer toggles a multi-line shortcut reference with `?` on an empty
// composer (bottom_pane/shortcut_overlay.rs) and closes it with `?` or Esc.
func TestShortcutOverlayLikeRust(t *testing.T) {
	model := NewModel(codextui.NewState(nil), Options{Width: 120, Height: 40})

	model.Update(keyRunes('?'))
	view := model.View()
	for _, want := range []string{
		"Keyboard shortcuts",
		"Compose",
		"Session",
		"Transcript (open first)",
		"/keymap customize",
		"? / esc close",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("shortcut overlay missing %q:\n%s", want, view)
		}
	}
	if !model.shortcutOverlay {
		t.Fatal("? on an empty composer should open the shortcut overlay")
	}
	if !model.composerIsEmpty() {
		t.Fatalf("composer should stay empty, got %q", model.composer.Value())
	}
	if strings.Contains(view, "? for shortcuts") {
		t.Fatalf("overlay should replace the passive hint row:\n%s", view)
	}

	model.Update(key(bubbletea.KeyEsc))
	if model.shortcutOverlay {
		t.Fatal("esc should close the shortcut overlay")
	}
	if view := model.View(); strings.Contains(view, "Keyboard shortcuts") {
		t.Fatalf("overlay still painted after esc:\n%s", view)
	} else if !strings.Contains(view, "? for shortcuts") {
		t.Fatalf("passive hint should return after esc:\n%s", view)
	}

	model.Update(keyRunes('?'))
	if !model.shortcutOverlay {
		t.Fatal("? should reopen the overlay")
	}
	model.Update(keyRunes('x'))
	if model.shortcutOverlay {
		t.Fatal("any other key should dismiss the overlay")
	}
	if got := model.composer.Value(); got != "x" {
		t.Fatalf("dismissing key should still reach the composer, got %q", got)
	}
}

func TestShortcutOverlayKeepsDraftTyping(t *testing.T) {
	model := NewModel(codextui.NewState(nil), Options{Width: 120, Height: 40})
	model.Update(keyRunes('h'))
	model.Update(keyRunes('?'))
	if model.shortcutOverlay {
		t.Fatal("? must not open the overlay while a draft exists")
	}
	if got := model.composer.Value(); got != "h?" {
		t.Fatalf("composer = %q, want %q", got, "h?")
	}
}

func TestShortcutOverlayStackedOnNarrowWidth(t *testing.T) {
	model := NewModel(codextui.NewState(nil), Options{Width: 60, Height: 40})
	model.Update(keyRunes('?'))
	view := model.View()
	for _, want := range []string{"Keyboard shortcuts", "Compose", "Session", "Transcript (open first)"} {
		if !strings.Contains(view, want) {
			t.Fatalf("narrow overlay missing %q:\n%s", want, view)
		}
	}
	for _, line := range strings.Split(view, "\n") {
		if width := codextui.DisplayWidth(ansiSequenceRE.ReplaceAllString(line, "")); width > 60 {
			t.Fatalf("overlay line width %d exceeds terminal: %q", width, line)
		}
	}
}

// Terminals with colour enabled wrap group titles in SGR codes. Column padding
// must be measured on visible cells, otherwise the header drifts left while the
// entry rows stay put.
func TestJoinShortcutOverlayColumnsIgnoresSGRWidth(t *testing.T) {
	groups := [][]string{
		{"\x1b[1mCompose\x1b[0m", "/        Commands"},
		{"\x1b[1mSession\x1b[0m", "tab            Send message"},
	}
	lines := joinShortcutOverlayColumns(groups, []int{26, 30})
	for _, line := range lines {
		plain := ansi.Strip(line)
		if index := strings.Index(plain, "Session"); index >= 0 && index != 30 {
			t.Fatalf("session column at %d, want 30: %q", index, plain)
		}
		if index := strings.Index(plain, "Send message"); index >= 0 && index != 45 {
			t.Fatalf("send message column at %d, want 45: %q", index, plain)
		}
	}
}
