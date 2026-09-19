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
