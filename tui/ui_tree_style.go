package tui

// Rust parity: codex-rs/tui/src/ui_tree/theme.rs. The wire tree carries typed
// colour tokens; this file validates them and resolves them against the active
// TextMate theme and the terminal colour depth.

import (
	"fmt"
	"strconv"
	"strings"
)

// UIColor is a validated wire colour token: a named ANSI colour, a semantic
// theme role, a status-line accent, or an exact "#rrggbb" value.
type UIColor string

const (
	// UIColorPrimary follows the theme's default foreground.
	UIColorPrimary UIColor = "primary"
	// UIColorSurface follows the theme's default background.
	UIColorSurface UIColor = "surface"
	// UIColorAccent follows the theme's accent colour.
	UIColorAccent UIColor = "accent"
	// UIColorBlack is ANSI slot 0.
	UIColorBlack UIColor = "black"
	// UIColorRed is ANSI slot 1.
	UIColorRed UIColor = "red"
	// UIColorGreen is ANSI slot 2.
	UIColorGreen UIColor = "green"
	// UIColorYellow is ANSI slot 3.
	UIColorYellow UIColor = "yellow"
	// UIColorBlue is ANSI slot 4.
	UIColorBlue UIColor = "blue"
	// UIColorMagenta is ANSI slot 5.
	UIColorMagenta UIColor = "magenta"
	// UIColorCyan is ANSI slot 6.
	UIColorCyan UIColor = "cyan"
	// UIColorWhite is ANSI slot 7 (ratatui's Gray).
	UIColorWhite UIColor = "white"
)

// UITextWeight is the wire text weight token.
type UITextWeight string

const (
	// UITextWeightNormal clears an inherited bold modifier.
	UITextWeightNormal UITextWeight = "normal"
	// UITextWeightBold sets the bold modifier.
	UITextWeightBold UITextWeight = "bold"
)

// UIRowWrap is the wire row wrapping token.
type UIRowWrap string

const (
	// UIRowWrapNowrap keeps a row on one line.
	UIRowWrapNowrap UIRowWrap = "nowrap"
	// UIRowWrapWrap moves children that do not fit to the next line.
	UIRowWrapWrap UIRowWrap = "wrap"
)

// UITextProps carries the optional text styles from the wire tree.
type UITextProps struct {
	Color     *UIColor
	Weight    *UITextWeight
	Italic    *bool
	Underline *bool
}

func (p UITextProps) isZero() bool {
	return p.Color == nil && p.Weight == nil && p.Italic == nil && p.Underline == nil
}

// UIStyle is the resolved style of one rendered span.
type UIStyle struct {
	Foreground UIColor
	Background UIColor
	Bold       bool
	Italic     bool
	Underline  bool
}

// UISpan is one styled run of rendered text.
type UISpan struct {
	Text  string
	Style UIStyle
}

var uiNamedColors = map[UIColor]uint8{
	UIColorBlack:   0,
	UIColorRed:     1,
	UIColorGreen:   2,
	UIColorYellow:  3,
	UIColorBlue:    4,
	UIColorMagenta: 5,
	UIColorCyan:    6,
	UIColorWhite:   7,
}

var uiStatusAccentScopes = map[UIColor][]string{
	"status-model":    {"entity.name.type", "support.type", "variable"},
	"status-path":     {"string", "markup.underline.link"},
	"status-branch":   {"entity.name.function", "entity.name.tag"},
	"status-state":    {"keyword.control", "keyword"},
	"status-usage":    {"constant.numeric", "constant"},
	"status-limit":    {"constant.language", "storage.type"},
	"status-metadata": {"comment", "constant.other"},
	"status-mode":     {"storage.modifier", "keyword.operator"},
	"status-thread":   {"markup.heading", "entity.name.section"},
	"status-progress": {"markup.inserted", "constant.numeric"},
}

var uiStatusAccentFallback = map[UIColor]uint8{
	"status-model":    6,
	"status-state":    6,
	"status-metadata": 6,
	"status-mode":     6,
	"status-path":     2,
	"status-usage":    2,
	"status-progress": 2,
	"status-branch":   5,
	"status-limit":    5,
	"status-thread":   5,
}

// ParseUIColor validates one wire colour token.
func ParseUIColor(raw string) (UIColor, error) {
	color := UIColor(raw)
	if _, ok := uiNamedColors[color]; ok {
		return color, nil
	}
	switch color {
	case UIColorPrimary, UIColorSurface, UIColorAccent:
		return color, nil
	}
	if _, ok := uiStatusAccentScopes[color]; ok {
		return color, nil
	}
	if len(raw) == 7 && raw[0] == '#' {
		if _, err := strconv.ParseUint(raw[1:], 16, 32); err == nil {
			return color, nil
		}
	}
	return "", fmt.Errorf("ui color %q is not supported", raw)
}

// ParseUITextWeight validates one wire weight token.
func ParseUITextWeight(raw string) (UITextWeight, error) {
	switch weight := UITextWeight(raw); weight {
	case UITextWeightNormal, UITextWeightBold:
		return weight, nil
	default:
		return "", fmt.Errorf("ui text weight %q is not supported", raw)
	}
}

// ParseUIRowWrap validates one wire row-wrap token.
func ParseUIRowWrap(raw string) (UIRowWrap, error) {
	switch wrap := UIRowWrap(raw); wrap {
	case UIRowWrapNowrap, UIRowWrapWrap:
		return wrap, nil
	default:
		return "", fmt.Errorf("ui row wrap %q is not supported", raw)
	}
}

func uiHexColor(raw UIColor) (RGB, bool) {
	if len(raw) != 7 || raw[0] != '#' {
		return RGB{}, false
	}
	parsed, err := strconv.ParseUint(string(raw[1:]), 16, 32)
	if err != nil {
		return RGB{}, false
	}
	return RGB{R: uint8(parsed >> 16), G: uint8(parsed >> 8), B: uint8(parsed)}, true
}

// uiTreeThemeColor resolves the theme's unscoped foreground or background.
func uiTreeThemeColor(themeID string, background bool) (TerminalColor, bool) {
	theme := loadCustomTheme(themeID)
	if theme == nil {
		theme = loadBundledTheme(themeID)
	}
	if theme == nil {
		return TerminalColor{}, false
	}
	raw := strings.TrimSpace(theme.Settings.Foreground)
	if background {
		raw = strings.TrimSpace(theme.Settings.Background)
	}
	color, ok := uiHexColor(UIColor(raw))
	if !ok {
		return TerminalColor{}, false
	}
	return TerminalColor{RGB: &color}, true
}

func uiIndexColor(index uint8) TerminalColor {
	return TerminalColor{Index: &index}
}

// uiTreeTerminalColor resolves one wire colour against the theme and terminal
// colour depth (Rust RenderTheme::resolve).
func uiTreeTerminalColor(themeID string, color UIColor, level StdoutColorLevel) TerminalColor {
	if index, ok := uiNamedColors[color]; ok {
		return uiIndexColor(index)
	}
	if rgb, ok := uiHexColor(color); ok {
		if level == ColorUnknown {
			return TerminalColor{}
		}
		return TerminalColor{RGB: &rgb}
	}
	switch color {
	case UIColorPrimary:
		if resolved, ok := uiTreeThemeColor(themeID, false); ok {
			return resolved
		}
		return TerminalColor{}
	case UIColorSurface:
		if resolved, ok := uiTreeThemeColor(themeID, true); ok {
			return resolved
		}
		return TerminalColor{}
	case UIColorAccent:
		if accent := ThemeAccentColor(themeID); accent != "" {
			if rgb, ok := uiHexColor(UIColor(accent)); ok {
				return TerminalColor{RGB: &rgb}
			}
		}
		return TerminalColor{}
	}
	if scopes, ok := uiStatusAccentScopes[color]; ok {
		if resolved, ok := themeScopeForeground(themeID, scopes...); ok {
			return resolved
		}
		if level == ColorUnknown {
			return TerminalColor{}
		}
		return uiIndexColor(uiStatusAccentFallback[color])
	}
	return TerminalColor{}
}

func uiStyleSGR(style UIStyle, themeID string, level StdoutColorLevel) string {
	codes := make([]string, 0, 5)
	if style.Bold {
		codes = append(codes, "1")
	}
	if style.Italic {
		codes = append(codes, "3")
	}
	if style.Underline {
		codes = append(codes, "4")
	}
	if style.Foreground != "" {
		codes = append(codes, sgrColorCodes(uiTreeTerminalColor(themeID, style.Foreground, level), true, level)...)
	}
	if style.Background != "" {
		codes = append(codes, sgrColorCodes(uiTreeTerminalColor(themeID, style.Background, level), false, level)...)
	}
	if len(codes) == 0 {
		return ""
	}
	return "\x1b[" + strings.Join(codes, ";") + "m"
}
