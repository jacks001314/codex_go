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

// AltKeyLabel is the name Rust's key hints give the alt modifier
// (codex-rs/tui/src/key_hint.rs ALT_LABEL): the option glyph on macOS, and
// `alt` everywhere else.
func AltKeyLabel() string {
	if runtime.GOOS == "darwin" {
		return "\u2325"
	}
	return "alt"
}

// ModifierLabelPrefix renders a modifier label followed by its separator the way
// Rust's `KeyBinding::display_label` does since #49136: bare glyph labels
// (\u2303 \u21e7 \u2325 \u2318 ^) attach directly to the key, while text labels
// (ctrl, shift, alt) keep the `+` separator. macOS therefore shows `\u2325t`
// instead of `\u2325+t`, while Linux keeps `alt+t`.
func ModifierLabelPrefix(label string) string {
	switch label {
	case "\u2303", "\u21e7", "\u2325", "\u2318", "^":
		return label
	}
	return label + "+"
}
