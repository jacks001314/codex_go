package tea

import (
	"strings"
	"testing"

	keymsg "codex_go/tui/keymsg"

	codextui "codex_go/tui"
)

// Rust parity: #53754 (21c1616119). Rust honours `super`/`cmd`/`command`
// shortcuts end to end. Bubble Tea v2 exposes ModSuper, so the key message keeps
// the modifier and the keymap runtime now produces `super-*` specs instead of
// dropping the press.
func TestSuperModifierRuntimeLikeRust(t *testing.T) {
	cases := []struct {
		name    string
		message keymsg.KeyMsg
		want    string
	}{
		{name: "super+a", message: keymsg.KeyMsg{Type: keymsg.KeyRunes, Runes: []rune{'a'}, Super: true}, want: "super-a"},
		{name: "super+left", message: keymsg.KeyMsg{Type: keymsg.KeyLeft, Super: true}, want: "super-left"},
		{name: "super+shift+a", message: keymsg.KeyMsg{Type: keymsg.KeyRunes, Runes: []rune{'A'}, Super: true}, want: "shift-super-a"},
		{name: "alt+super+a", message: keymsg.KeyMsg{Type: keymsg.KeyRunes, Runes: []rune{'a'}, Alt: true, Super: true}, want: "alt-super-a"},
		{name: "ctrl+super+k", message: keymsg.KeyMsg{Type: keymsg.KeyCtrlK, Super: true}, want: "ctrl-super-k"},
		{name: "plain a", message: keymsg.KeyMsg{Type: keymsg.KeyRunes, Runes: []rune{'a'}}, want: "a"},
		{name: "alt+a", message: keymsg.KeyMsg{Type: keymsg.KeyRunes, Runes: []rune{'a'}, Alt: true}, want: "alt-a"},
	}
	for _, testCase := range cases {
		if got := KeySpecFromKeyMsg(testCase.message); got != testCase.want {
			t.Fatalf("%s: KeySpecFromKeyMsg = %q, want %q", testCase.name, got, testCase.want)
		}
	}

	// The keymap inspector reports the modifier the terminal delivered.
	super := keymsg.KeyMsg{Type: keymsg.KeyRunes, Runes: []rune{'a'}, Super: true}
	if raw := keymapInspectorRawEvent(super); !strings.Contains(raw, "modifiers=super") {
		t.Fatalf("inspector raw event = %q, want the super modifier", raw)
	}
	if label := keymapInspectorDetectedLabel(super); label != "super+a" {
		t.Fatalf("inspector detected label = %q, want super+a", label)
	}

	// The configuration layer stays portable: bindings captured on a platform
	// whose terminal reports super still parse and validate here.
	for _, spec := range []string{"cmd-left", "command-left", "super-left"} {
		got, err := codextui.NormalizeKeybindingSpec(spec)
		if err != nil || got != "super-left" {
			t.Fatalf("NormalizeKeybindingSpec(%q) = %q, %v", spec, got, err)
		}
	}
}
