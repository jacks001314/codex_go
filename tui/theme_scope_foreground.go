package tui

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/alecthomas/chroma/v2"
	chromastyles "github.com/alecthomas/chroma/v2/styles"
)

// Rust parity: tui/src/render/highlight.rs `foreground_style_for_scopes_with_theme`
// (plus `convert_syntect_color`). A caller that needs a theme-derived colour (the
// Mermaid diagram roles) asks for a list of TextMate scopes and takes the first
// one the resolved theme styles with a foreground colour.
//
// Rust renders the resolved colour through ratatui unconditionally (there is no
// colour-level gate in the Mermaid path), so this emits the theme's rgb as a
// truecolor sequence and a palette-encoded theme as an indexed sequence.

// ThemeScopeForegroundSGR renders the theme's foreground colour for the first
// matching scope as an SGR prefix. It returns "" when the theme styles none of
// the scopes or the scope resolves to the terminal default, so the caller keeps
// its own fallback.
func ThemeScopeForegroundSGR(themeID string, scopes ...string) string {
	colour, ok := themeScopeForeground(themeID, scopes...)
	if !ok {
		return ""
	}
	if colour.Index != nil {
		return fmt.Sprintf("\x1b[38;5;%dm", *colour.Index)
	}
	if colour.RGB != nil {
		return fmt.Sprintf("\x1b[38;2;%d;%d;%dm", colour.RGB.R, colour.RGB.G, colour.RGB.B)
	}
	return ""
}

// themeScopeForeground resolves a theme's foreground colour for the first scope
// it styles. A custom `.tmTheme` wins over the bundled asset, and a chroma-mapped
// theme falls back to the resolved style's own token colour (skipping a token
// that only inherits the default text colour, which would otherwise mask the
// caller's fallback).
func themeScopeForeground(themeID string, scopes ...string) (TerminalColor, bool) {
	id := strings.ToLower(strings.TrimSpace(themeID))
	if id == "" {
		return TerminalColor{}, false
	}
	if theme := loadCustomTheme(id); theme != nil {
		// A parsed theme is authoritative: a scope it leaves to the terminal
		// default has no colour, so Rust falls through to the caller's fallback
		// rather than borrowing another source.
		return theme.scopeForeground(scopes...)
	}
	if theme := loadBundledTheme(id); theme != nil {
		return theme.scopeForeground(scopes...)
	}
	style := chromastyles.Get(ChromaThemeForCodexTheme(id))
	defaultText := style.Get(chroma.Text).Colour
	for _, scope := range scopes {
		token, ok := tmThemeSelectorToken(scope)
		if !ok {
			continue
		}
		colour := style.Get(token).Colour
		if colour.IsSet() && colour != defaultText {
			return TerminalColor{RGB: &RGB{R: uint8(colour.Red()), G: uint8(colour.Green()), B: uint8(colour.Blue())}}, true
		}
	}
	return TerminalColor{}, false
}

// scopeForeground returns the theme's foreground for the first scope whose
// TextMate selector matches (equal, or a dotted prefix as syntect matches a
// scope stack), decoding Rust's bat-compatible alpha encodings.
func (t *tmTheme) scopeForeground(scopes ...string) (TerminalColor, bool) {
	if t == nil {
		return TerminalColor{}, false
	}
	for _, scope := range scopes {
		for _, scoped := range t.Scopes {
			for _, selector := range strings.Split(scoped.Scope, ",") {
				if !scopeSelectorMatches(strings.TrimSpace(selector), scope) {
					continue
				}
				colour, ok := themeForegroundColor(scoped.Settings.Foreground)
				if !ok {
					// alpha 0x01 is the terminal default: the scope is styled
					// without a colour, so Rust tries the next scope.
					continue
				}
				return colour, true
			}
		}
	}
	return TerminalColor{}, false
}

// themeForegroundColor decodes a theme foreground, mirroring Rust's
// `convert_syntect_color`: alpha 0x00 stores an ANSI palette index in red
// (bat-compatible themes), alpha 0x01 is the terminal default, and any other
// alpha is plain rgb.
func themeForegroundColor(raw string) (TerminalColor, bool) {
	value := strings.TrimPrefix(strings.TrimSpace(raw), "#")
	if len(value) == 8 {
		rgb, err := parseHex(value[:6])
		if err != nil {
			return TerminalColor{}, false
		}
		alpha, err := parseHex(value[6:])
		if err != nil {
			return TerminalColor{}, false
		}
		switch alpha {
		case 0x00:
			index := uint8(rgb >> 16)
			return TerminalColor{Index: &index}, true
		case 0x01:
			return TerminalColor{}, false
		}
		return TerminalColor{RGB: &RGB{R: uint8(rgb >> 16), G: uint8(rgb >> 8), B: uint8(rgb)}}, true
	}
	colour := tmThemeColour(raw)
	r, g, b, ok := ParseThemeAccentHex(colour)
	if !ok {
		return TerminalColor{}, false
	}
	return TerminalColor{RGB: &RGB{R: r, G: g, B: b}}, true
}

func scopeSelectorMatches(selector string, scope string) bool {
	selector = strings.ToLower(strings.TrimSpace(selector))
	scope = strings.ToLower(strings.TrimSpace(scope))
	if selector == "" || scope == "" {
		return false
	}
	return scope == selector || strings.HasPrefix(scope, selector+".")
}

func parseHex(value string) (uint32, error) {
	parsed, err := strconv.ParseUint(value, 16, 32)
	return uint32(parsed), err
}
