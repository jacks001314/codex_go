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
	if got := AltKey('x').Label(); got != "alt + x" {
		t.Fatalf("alt label = %q", got)
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
