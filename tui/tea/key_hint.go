package tea

import (
	"strings"
	"unicode"

	bubbletea "github.com/charmbracelet/bubbletea"

	"codex_go/tui"
)

// Rust parity: codex-rs/tui/src/key_hint.rs.

type KeyBinding struct {
	Type  bubbletea.KeyType
	Rune  rune
	Alt   bool
	Shift bool
}

func PlainKey(keyType bubbletea.KeyType) KeyBinding {
	return KeyBinding{Type: keyType}
}

func CharKey(r rune) KeyBinding {
	return KeyBinding{Type: bubbletea.KeyRunes, Rune: unicode.ToLower(r), Shift: unicode.IsUpper(r)}
}

func AltKey(r rune) KeyBinding {
	return KeyBinding{Type: bubbletea.KeyRunes, Rune: unicode.ToLower(r), Alt: true}
}

func ShiftKey(r rune) KeyBinding {
	return KeyBinding{Type: bubbletea.KeyRunes, Rune: unicode.ToLower(r), Shift: true}
}

func (b KeyBinding) IsPress(message bubbletea.KeyMsg) bool {
	if b.Type != bubbletea.KeyRunes {
		return message.Type == b.Type && message.Alt == b.Alt
	}
	if message.Type != bubbletea.KeyRunes || len(message.Runes) != 1 || message.Alt != b.Alt {
		return false
	}
	r := message.Runes[0]
	if b.Shift {
		return unicode.ToLower(r) == b.Rune && unicode.IsUpper(r)
	}
	return unicode.ToLower(r) == b.Rune && !unicode.IsUpper(r)
}

// Label renders the binding the way Rust's `KeyBinding::display_label` does: a
// compact shortcut whose modifiers (control, shift, alt) precede the key, each
// joined with a bare `+` (#46680).
func (b KeyBinding) Label() string {
	var label strings.Builder
	if IsCtrlKeyType(b.Type) {
		label.WriteString("ctrl+")
	}
	if b.Shift {
		label.WriteString("shift+")
	}
	if b.Alt {
		label.WriteString(AltKeyLabel())
		label.WriteString("+")
	}
	key := keyTypeLabel(b.Type)
	if b.Type == bubbletea.KeyRunes {
		key = string(b.Rune)
		if b.Rune == ' ' {
			key = "space"
		}
	}
	label.WriteString(key)
	return label.String()
}

// AltKeyLabel is the name Rust gives the alt modifier: the option glyph on
// macOS, otherwise `alt`.
func AltKeyLabel() string {
	return tui.AltKeyLabel()
}

// IsCtrlKeyType reports whether a key type is a control chord, the way Rust's
// KeyModifiers::CONTROL is carried.
func IsCtrlKeyType(keyType bubbletea.KeyType) bool {
	if _, named := namedKeyLabel(keyType); named {
		// bubbletea aliases several control chords onto named keys (ctrl+i is
		// tab, ctrl+m is enter); Rust names those keys instead of their chord.
		return false
	}
	_, ok := ctrlKeyNames[keyType]
	return ok
}

func IsPlainTextKey(message bubbletea.KeyMsg) bool {
	return message.Type == bubbletea.KeyRunes &&
		!message.Alt &&
		len(message.Runes) > 0 &&
		!unicode.IsControl(message.Runes[0])
}

func AnyKeyPressed(bindings []KeyBinding, message bubbletea.KeyMsg) bool {
	for _, binding := range bindings {
		if binding.IsPress(message) {
			return true
		}
	}
	return false
}

// ctrlKeyNames maps a control chord's key type to the key it names, so the
// control modifier can be rendered once in Rust's modifier order.
var ctrlKeyNames = map[bubbletea.KeyType]string{
	bubbletea.KeyCtrlA: "a", bubbletea.KeyCtrlB: "b", bubbletea.KeyCtrlC: "c",
	bubbletea.KeyCtrlD: "d", bubbletea.KeyCtrlE: "e", bubbletea.KeyCtrlF: "f",
	bubbletea.KeyCtrlG: "g", bubbletea.KeyCtrlH: "h", bubbletea.KeyCtrlI: "i",
	bubbletea.KeyCtrlJ: "j", bubbletea.KeyCtrlK: "k", bubbletea.KeyCtrlL: "l",
	bubbletea.KeyCtrlM: "m", bubbletea.KeyCtrlN: "n", bubbletea.KeyCtrlO: "o",
	bubbletea.KeyCtrlP: "p", bubbletea.KeyCtrlQ: "q", bubbletea.KeyCtrlR: "r",
	bubbletea.KeyCtrlS: "s", bubbletea.KeyCtrlT: "t", bubbletea.KeyCtrlU: "u",
	bubbletea.KeyCtrlV: "v", bubbletea.KeyCtrlW: "w", bubbletea.KeyCtrlX: "x",
	bubbletea.KeyCtrlY: "y", bubbletea.KeyCtrlZ: "z",
}

// keyTypeLabel renders the key itself, without its modifiers: Rust's
// `display_label` tail (arrow glyphs, `enter`, `space`, `pgup`, ...).
func keyTypeLabel(keyType bubbletea.KeyType) string {
	if label, named := namedKeyLabel(keyType); named {
		return label
	}
	if name, ok := ctrlKeyNames[keyType]; ok {
		return name
	}
	return strings.ToLower(keyType.String())
}

// namedKeyLabel reports the label of a key Rust names instead of spelling out
// its key code.
func namedKeyLabel(keyType bubbletea.KeyType) (string, bool) {
	switch keyType {
	case bubbletea.KeyEnter:
		return "enter", true
	case bubbletea.KeyEsc:
		return "esc", true
	case bubbletea.KeyTab:
		return "tab", true
	case bubbletea.KeyUp:
		return "↑", true
	case bubbletea.KeyDown:
		return "↓", true
	case bubbletea.KeyLeft:
		return "←", true
	case bubbletea.KeyRight:
		return "→", true
	case bubbletea.KeyPgUp:
		return "pgup", true
	case bubbletea.KeyPgDown:
		return "pgdn", true
	case bubbletea.KeySpace:
		return "space", true
	case bubbletea.KeyDelete:
		// Rust renders `del` in its instrumented builds; production crossterm
		// spellings are not part of the label surface Go reproduces.
		return "del", true
	}
	return "", false
}
