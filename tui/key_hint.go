package tui

import "runtime"

type KeyHint struct {
	Key   string
	Label string
}

func KeyHintText(h KeyHint) string {
	if h.Key == "" {
		return h.Label
	}
	return h.Key + " " + h.Label
}

// KeyModifier names one entry of Rust's MODIFIER_LABELS table
// (codex-rs/tui/src/key_hint.rs).
type KeyModifier string

const (
	KeyModifierControl KeyModifier = "control"
	KeyModifierShift   KeyModifier = "shift"
	KeyModifierAlt     KeyModifier = "alt"
)

// ModifierKeyLabelForPlatform returns the label Rust's MODIFIER_LABELS table
// assigns to one modifier (Rust #49804, codex-rs/tui/src/key_hint.rs): the
// glyphs ⌃ / ⇧ / ⌥ on macOS, `^` for control on Linux, and the textual
// ctrl / shift / alt everywhere else. Rust compiles the glyph table under
// `cfg(any(test, target_os = "macos"))`, so the table is parameterized here to
// stay assertable on every platform.
func ModifierKeyLabelForPlatform(modifier KeyModifier, goos string) string {
	switch modifier {
	case KeyModifierControl:
		switch goos {
		case "darwin":
			return "\u2303"
		case "linux":
			return "^"
		default:
			return "ctrl"
		}
	case KeyModifierShift:
		if goos == "darwin" {
			return "\u21e7"
		}
		return "shift"
	case KeyModifierAlt:
		if goos == "darwin" {
			return "\u2325"
		}
		return "alt"
	}
	return ""
}

// ControlKeyLabel is the control modifier label for the host platform: `⌃` on
// macOS, `^` on Linux, `ctrl` elsewhere.
func ControlKeyLabel() string {
	return ModifierKeyLabelForPlatform(KeyModifierControl, runtime.GOOS)
}

// ShiftKeyLabel is the shift modifier label for the host platform: `⇧` on
// macOS, `shift` elsewhere.
func ShiftKeyLabel() string {
	return ModifierKeyLabelForPlatform(KeyModifierShift, runtime.GOOS)
}

// AltKeyLabel is the name Rust's key hints give the alt modifier
// (codex-rs/tui/src/key_hint.rs MODIFIER_LABELS): the option glyph on macOS, and
// `alt` everywhere else.
func AltKeyLabel() string {
	return ModifierKeyLabelForPlatform(KeyModifierAlt, runtime.GOOS)
}

// ModifierLabelPrefix renders a modifier label followed by its separator the way
// Rust's `KeyBinding::display_label` does since #49136: bare glyph labels
// (\u2303 \u21e7 \u2325 \u2318 ^) attach directly to the key, while text labels
// (ctrl, shift, alt) keep the `+` separator. macOS therefore shows `\u2325t`
// instead of `\u2325+t`, while Linux keeps `alt+t` and renders control as `^t`
// (Rust #49804).
func ModifierLabelPrefix(label string) string {
	switch label {
	case "\u2303", "\u21e7", "\u2325", "\u2318", "^":
		return label
	}
	return label + "+"
}
