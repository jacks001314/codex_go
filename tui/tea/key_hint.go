package tea

import (
	"strings"
	"unicode"

	bubbletea "github.com/charmbracelet/bubbletea"
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

func (b KeyBinding) Label() string {
	parts := []string{}
	if b.Alt {
		parts = append(parts, "alt")
	}
	if b.Shift {
		parts = append(parts, "shift")
	}
	key := keyTypeLabel(b.Type)
	if b.Type == bubbletea.KeyRunes {
		key = string(b.Rune)
		if b.Rune == ' ' {
			key = "space"
		}
	}
	parts = append(parts, key)
	return strings.Join(parts, " + ")
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

func keyTypeLabel(keyType bubbletea.KeyType) string {
	switch keyType {
	case bubbletea.KeyEnter:
		return "enter"
	case bubbletea.KeyEsc:
		return "esc"
	case bubbletea.KeyUp:
		return "up"
	case bubbletea.KeyDown:
		return "down"
	case bubbletea.KeyLeft:
		return "left"
	case bubbletea.KeyRight:
		return "right"
	case bubbletea.KeyPgUp:
		return "pgup"
	case bubbletea.KeyPgDown:
		return "pgdn"
	case bubbletea.KeyCtrlC:
		return "ctrl + c"
	case bubbletea.KeyCtrlD:
		return "ctrl + d"
	default:
		return strings.ToLower(keyType.String())
	}
}
