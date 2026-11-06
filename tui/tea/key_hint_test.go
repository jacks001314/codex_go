package tea

import (
	"testing"

	"codex_go/tui"
	"codex_go/tui/keymsg"
)

func TestKeyBindingMatchesShiftedUppercase(t *testing.T) {
	binding := ShiftKey('a')
	if !binding.IsPress(keymsg.KeyMsg{Type: keymsg.KeyRunes, Runes: []rune{'A'}}) {
		t.Fatal("shift binding should match uppercase rune")
	}
	if binding.IsPress(keymsg.KeyMsg{Type: keymsg.KeyRunes, Runes: []rune{'a'}}) {
		t.Fatal("shift binding should not match plain lowercase")
	}
}

func TestKeyBindingLabelsAndPlainTextBoundary(t *testing.T) {
	if got := PlainKey(keymsg.KeyEnter).Label(); got != "enter" {
		t.Fatalf("enter label = %q", got)
	}
	// Rust #46680 renders a compact shortcut: modifiers in control/shift/alt
	// order, joined with a bare `+`.
	if got := AltKey('x').Label(); got != "alt+x" {
		t.Fatalf("alt label = %q", got)
	}
	// Rust #49804: control (and shift) render through the platform modifier
	// table, so Linux shows `^t` while macOS shows `\u2303t`.
	ctrl := tui.ModifierLabelPrefix(tui.ControlKeyLabel())
	if got := PlainKey(keymsg.KeyCtrlT).Label(); got != ctrl+"t" {
		t.Fatalf("ctrl label = %q, want %q", got, ctrl+"t")
	}
	shift := tui.ModifierLabelPrefix(tui.ShiftKeyLabel())
	wantChord := ctrl + shift + tui.ModifierLabelPrefix(AltKeyLabel()) + "t"
	if got := (KeyBinding{Type: keymsg.KeyCtrlT, Shift: true, Alt: true}).Label(); got != wantChord {
		t.Fatalf("chord label = %q, want %q", got, wantChord)
	}
	if got := PlainKey(keymsg.KeyUp).Label(); got != "\u2191" {
		t.Fatalf("arrow label = %q", got)
	}
	if got := PlainKey(keymsg.KeySpace).Label(); got != "space" {
		t.Fatalf("space label = %q", got)
	}
	if !IsPlainTextKey(keymsg.KeyMsg{Type: keymsg.KeyRunes, Runes: []rune{'j'}}) {
		t.Fatal("plain j should be text")
	}
	if IsPlainTextKey(keymsg.KeyMsg{Type: keymsg.KeyRunes, Runes: []rune{'j'}, Alt: true}) {
		t.Fatal("alt+j should not be plain text")
	}
	if IsPlainTextKey(keymsg.KeyMsg{Type: keymsg.KeyCtrlC}) {
		t.Fatal("ctrl+c should not be plain text")
	}
}

func TestAnyKeyPressed(t *testing.T) {
	bindings := []KeyBinding{PlainKey(keymsg.KeyUp), CharKey('x')}
	if !AnyKeyPressed(bindings, keymsg.KeyMsg{Type: keymsg.KeyRunes, Runes: []rune{'x'}}) {
		t.Fatal("expected x binding to match")
	}
	if AnyKeyPressed(bindings, keymsg.KeyMsg{Type: keymsg.KeyRunes, Runes: []rune{'y'}}) {
		t.Fatal("did not expect y binding to match")
	}
}
