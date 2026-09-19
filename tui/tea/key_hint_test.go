package tea

import (
	"testing"

	bubbletea "github.com/charmbracelet/bubbletea"
)

func TestKeyBindingMatchesShiftedUppercase(t *testing.T) {
	binding := ShiftKey('a')
	if !binding.IsPress(bubbletea.KeyMsg{Type: bubbletea.KeyRunes, Runes: []rune{'A'}}) {
		t.Fatal("shift binding should match uppercase rune")
	}
	if binding.IsPress(bubbletea.KeyMsg{Type: bubbletea.KeyRunes, Runes: []rune{'a'}}) {
		t.Fatal("shift binding should not match plain lowercase")
	}
}

func TestKeyBindingLabelsAndPlainTextBoundary(t *testing.T) {
	if got := PlainKey(bubbletea.KeyEnter).Label(); got != "enter" {
		t.Fatalf("enter label = %q", got)
	}
	// Rust #46680 renders a compact shortcut: modifiers in control/shift/alt
	// order, joined with a bare `+`.
	if got := AltKey('x').Label(); got != "alt+x" {
		t.Fatalf("alt label = %q", got)
	}
	if got := PlainKey(bubbletea.KeyCtrlT).Label(); got != "ctrl+t" {
		t.Fatalf("ctrl label = %q", got)
	}
	if got := (KeyBinding{Type: bubbletea.KeyCtrlT, Shift: true, Alt: true}).Label(); got != "ctrl+shift+"+AltKeyLabel()+"+t" {
		t.Fatalf("chord label = %q", got)
	}
	if got := PlainKey(bubbletea.KeyUp).Label(); got != "\u2191" {
		t.Fatalf("arrow label = %q", got)
	}
	if got := PlainKey(bubbletea.KeySpace).Label(); got != "space" {
		t.Fatalf("space label = %q", got)
	}
	if !IsPlainTextKey(bubbletea.KeyMsg{Type: bubbletea.KeyRunes, Runes: []rune{'j'}}) {
		t.Fatal("plain j should be text")
	}
	if IsPlainTextKey(bubbletea.KeyMsg{Type: bubbletea.KeyRunes, Runes: []rune{'j'}, Alt: true}) {
		t.Fatal("alt+j should not be plain text")
	}
	if IsPlainTextKey(bubbletea.KeyMsg{Type: bubbletea.KeyCtrlC}) {
		t.Fatal("ctrl+c should not be plain text")
	}
}

func TestAnyKeyPressed(t *testing.T) {
	bindings := []KeyBinding{PlainKey(bubbletea.KeyUp), CharKey('x')}
	if !AnyKeyPressed(bindings, bubbletea.KeyMsg{Type: bubbletea.KeyRunes, Runes: []rune{'x'}}) {
		t.Fatal("expected x binding to match")
	}
	if AnyKeyPressed(bindings, bubbletea.KeyMsg{Type: bubbletea.KeyRunes, Runes: []rune{'y'}}) {
		t.Fatal("did not expect y binding to match")
	}
}
