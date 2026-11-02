package tea

import (
	"reflect"
	"strings"
	"testing"

	bubbletea "github.com/charmbracelet/bubbletea"

	codextui "codex_go/tui"
)

// Rust parity: #53754 (21c1616119). Rust can honour `super`/`cmd`/`command`
// shortcuts end to end. In Go the keymap layer already accepts the bindings
// (codextui.NormalizeKeybindingSpec, tui/keymap_config.go:492), but the runtime
// half is blocked at the input layer: bubbletea v1 KeyMsg carries
// Type/Runes/Alt/Paste only (bubbletea@v1.3.10/key.go:52-58), so
// keySpecFromKeyMsg (tui/tea/keymap_runtime.go:339) cannot produce a `super-*`
// spec and the capture popup reports the key as unsupported. This guard test
// pins that boundary: when the dependency exposes a super modifier the test
// fails, which is the signal to wire super-editor shortcuts through instead of
// keeping this green.
func TestSuperModifierRuntimeBoundaryLikeRust(t *testing.T) {
	keyType := reflect.TypeOf(bubbletea.Key{})
	fields := map[string]bool{}
	for i := 0; i < keyType.NumField(); i++ {
		fields[strings.ToLower(keyType.Field(i).Name)] = true
	}
	for _, modifier := range []string{"super", "cmd", "command", "hyper", "meta", "mod", "modifiers", "modifier"} {
		if fields[modifier] {
			t.Fatalf("bubbletea.Key now exposes %q: wire super bindings into the runtime instead of keeping this guard", modifier)
		}
	}
	for _, required := range []string{"type", "runes", "alt", "paste"} {
		if !fields[required] {
			t.Fatalf("bubbletea.Key lost field %q; revisit the super-modifier probe", required)
		}
	}

	// Alt is the only modifier the key message can express, so a super press has
	// no KeyMsg shape to route through the keymap runtime.
	if got := KeySpecFromKeyMsg(bubbletea.KeyMsg{Type: bubbletea.KeyRunes, Runes: []rune{'a'}, Alt: true}); got != "alt-a" {
		t.Fatalf("alt spec = %q, want alt-a", got)
	}
	if got := KeySpecFromKeyMsg(bubbletea.KeyMsg{Type: bubbletea.KeyRunes, Runes: []rune{'a'}}); got != "a" {
		t.Fatalf("plain spec = %q, want a", got)
	}
	if strings.HasPrefix(KeySpecFromKeyMsg(bubbletea.KeyMsg{Type: bubbletea.KeyLeft, Alt: true}), "super") {
		t.Fatal("the key spec builder invented a super modifier")
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
