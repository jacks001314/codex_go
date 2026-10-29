package tui

import (
	"strconv"
	"strings"
)

// Rust parity: the syntax themes Rust bundles come from two-face (bat's theme
// set), whose `.tmTheme` selector lists style Markdown raw-text scopes. Go's
// syntax highlighting resolves bundled non-`.tmTheme` themes through Chroma
// approximations, which cannot express those selector lists, so the resolved
// foregrounds for the scopes the renderer asks about are recorded here.
//
// Generated from two-face 0.5.1 (bat 0.26.1) by resolving syntect's
// `Highlighter::style_mod_for_stack` for each scope - exactly what Rust's
// `foreground_style_for_scopes_with_theme` does - so regenerate this table when
// the Rust dependency changes.
//
// Value encoding: "#rrggbb" for a truecolor theme, "ansi:N" for a theme that
// carries an ANSI palette index (bat encodes the index in the colour alpha), and
// "" when the theme styles no foreground for the scope, in which case Rust falls
// through to the next scope and finally to its own fallback.

// batRawInlineForegrounds records Rust's MarkdownStyles::code foregrounds,
// resolved from `markup.inline.raw.string.markdown` (which catppuccin styles as
// inline-code text) and `markup.raw.inline.markdown` (which the other bat themes
// style).
type batRawInlineForegrounds struct {
	InlineCode string
	InlineRaw  string
}

var batThemeRawInlineForegrounds = map[string]batRawInlineForegrounds{
	"ansi":                    {InlineRaw: "ansi:2"},
	"base16":                  {InlineRaw: "ansi:2"},
	"base16-eighties-dark":    {InlineRaw: "#99cc99"},
	"base16-mocha-dark":       {InlineRaw: "#beb55b"},
	"base16-ocean-dark":       {InlineRaw: "#a3be8c"},
	"base16-ocean-light":      {InlineRaw: "#a3be8c"},
	"base16-256":              {InlineRaw: "ansi:2"},
	"catppuccin-frappe":       {InlineCode: "#a6d189"},
	"catppuccin-latte":        {InlineCode: "#40a02b"},
	"catppuccin-macchiato":    {InlineCode: "#a6da95"},
	"catppuccin-mocha":        {InlineCode: "#a6e3a1"},
	"coldark-cold":            {InlineCode: "#006d6d", InlineRaw: "#006d6d"},
	"coldark-dark":            {InlineCode: "#66cccc", InlineRaw: "#66cccc"},
	"dark-neon":               {InlineRaw: "#578bb3"},
	"dracula":                 {},
	"github":                  {},
	"gruvbox-dark":            {InlineRaw: "#fabd2f"},
	"gruvbox-light":           {InlineRaw: "#b57614"},
	"inspired-github":         {},
	"1337":                    {},
	"monokai-extended":        {},
	"monokai-extended-bright": {},
	"monokai-extended-light":  {},
	"monokai-extended-origin": {},
	"nord":                    {InlineRaw: "#8fbcbb"},
	"one-half-dark":           {InlineRaw: "#98c379"},
	"one-half-light":          {InlineRaw: "#50a14f"},
	"solarized-dark":          {},
	"solarized-light":         {},
	"sublime-snazzy":          {},
	"two-dark":                {InlineRaw: "#98c379"},
	"zenburn":                 {InlineRaw: "#fed6af"},
}

// batThemeRawInlineForeground resolves one of the raw-inline scopes for a
// bundled bat theme. known reports whether the theme is part of the bat bundle,
// so an unstyled scope stops the search instead of falling back to a Chroma
// approximation the theme never defined.
func batThemeRawInlineForeground(themeID string, scope string) (TerminalColor, bool, bool) {
	foregrounds, known := batThemeRawInlineForegrounds[themeID]
	if !known {
		return TerminalColor{}, false, false
	}
	var value string
	switch scope {
	case "markup.inline.raw.string.markdown":
		value = foregrounds.InlineCode
	case "markup.raw.inline.markdown":
		value = foregrounds.InlineRaw
	default:
		return TerminalColor{}, false, false
	}
	colour, ok := batRawInlineColor(value)
	return colour, ok, true
}

func batRawInlineColor(value string) (TerminalColor, bool) {
	if value == "" {
		return TerminalColor{}, false
	}
	if encoded, found := strings.CutPrefix(value, "ansi:"); found {
		parsed, err := strconv.Atoi(encoded)
		if err != nil || parsed < 0 || parsed > 255 {
			return TerminalColor{}, false
		}
		index := uint8(parsed)
		return TerminalColor{Index: &index}, true
	}
	return themeForegroundColor(value)
}
