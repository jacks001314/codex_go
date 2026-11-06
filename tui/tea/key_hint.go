package tea

import (
	"strings"
	"unicode"

	"codex_go/tui"
	"codex_go/tui/keymsg"
)

// Rust parity: codex-rs/tui/src/key_hint.rs.

type KeyBinding struct {
	Type  keymsg.KeyType
	Rune  rune
	Alt   bool
	Shift bool
}

func PlainKey(keyType keymsg.KeyType) KeyBinding {
	return KeyBinding{Type: keyType}
}

func CharKey(r rune) KeyBinding {
	return KeyBinding{Type: keymsg.KeyRunes, Rune: unicode.ToLower(r), Shift: unicode.IsUpper(r)}
}

func AltKey(r rune) KeyBinding {
	return KeyBinding{Type: keymsg.KeyRunes, Rune: unicode.ToLower(r), Alt: true}
}

func ShiftKey(r rune) KeyBinding {
	return KeyBinding{Type: keymsg.KeyRunes, Rune: unicode.ToLower(r), Shift: true}
}

func (b KeyBinding) IsPress(message keymsg.KeyMsg) bool {
	if b.Type != keymsg.KeyRunes {
		return message.Type == b.Type && message.Alt == b.Alt
	}
	if message.Type != keymsg.KeyRunes || len(message.Runes) != 1 || message.Alt != b.Alt {
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
		// Rust #49804: the control label is platform-specific (^ on Linux,
		// ⌃ on macOS, ctrl elsewhere) and follows the #49136 separator rule.
		label.WriteString(tui.ModifierLabelPrefix(tui.ControlKeyLabel()))
	}
	if b.Shift {
		label.WriteString(tui.ModifierLabelPrefix(tui.ShiftKeyLabel()))
	}
	if b.Alt {
		// Rust #49136: the option glyph attaches without a `+`.
		label.WriteString(tui.ModifierLabelPrefix(AltKeyLabel()))
	}
	key := keyTypeLabel(b.Type)
	if b.Type == keymsg.KeyRunes {
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
func IsCtrlKeyType(keyType keymsg.KeyType) bool {
	if _, named := namedKeyLabel(keyType); named {
		// bubbletea aliases several control chords onto named keys (ctrl+i is
		// tab, ctrl+m is enter); Rust names those keys instead of their chord.
		return false
	}
	_, ok := ctrlKeyNames[keyType]
	return ok
}

func IsPlainTextKey(message keymsg.KeyMsg) bool {
	return message.Type == keymsg.KeyRunes &&
		!message.Alt &&
		len(message.Runes) > 0 &&
		!unicode.IsControl(message.Runes[0])
}

func AnyKeyPressed(bindings []KeyBinding, message keymsg.KeyMsg) bool {
	for _, binding := range bindings {
		if binding.IsPress(message) {
			return true
		}
	}
	return false
}

// ctrlKeyNames maps a control chord's key type to the key it names, so the
// control modifier can be rendered once in Rust's modifier order.
var ctrlKeyNames = map[keymsg.KeyType]string{
	keymsg.KeyCtrlA: "a", keymsg.KeyCtrlB: "b", keymsg.KeyCtrlC: "c",
	keymsg.KeyCtrlD: "d", keymsg.KeyCtrlE: "e", keymsg.KeyCtrlF: "f",
	keymsg.KeyCtrlG: "g", keymsg.KeyCtrlH: "h", keymsg.KeyCtrlI: "i",
	keymsg.KeyCtrlJ: "j", keymsg.KeyCtrlK: "k", keymsg.KeyCtrlL: "l",
	keymsg.KeyCtrlM: "m", keymsg.KeyCtrlN: "n", keymsg.KeyCtrlO: "o",
	keymsg.KeyCtrlP: "p", keymsg.KeyCtrlQ: "q", keymsg.KeyCtrlR: "r",
	keymsg.KeyCtrlS: "s", keymsg.KeyCtrlT: "t", keymsg.KeyCtrlU: "u",
	keymsg.KeyCtrlV: "v", keymsg.KeyCtrlW: "w", keymsg.KeyCtrlX: "x",
	keymsg.KeyCtrlY: "y", keymsg.KeyCtrlZ: "z",
}

// keyTypeLabel renders the key itself, without its modifiers: Rust's
// `display_label` tail (arrow glyphs, `enter`, `space`, `pgup`, ...).
func keyTypeLabel(keyType keymsg.KeyType) string {
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
func namedKeyLabel(keyType keymsg.KeyType) (string, bool) {
	switch keyType {
	case keymsg.KeyEnter:
		return "enter", true
	case keymsg.KeyEsc:
		return "esc", true
	case keymsg.KeyTab:
		return "tab", true
	case keymsg.KeyUp:
		return "↑", true
	case keymsg.KeyDown:
		return "↓", true
	case keymsg.KeyLeft:
		return "←", true
	case keymsg.KeyRight:
		return "→", true
	case keymsg.KeyPgUp:
		return "pgup", true
	case keymsg.KeyPgDown:
		return "pgdn", true
	case keymsg.KeySpace:
		return "space", true
	case keymsg.KeyDelete:
		// Rust renders `del` in its instrumented builds; production crossterm
		// spellings are not part of the label surface Go reproduces.
		return "del", true
	}
	return "", false
}
