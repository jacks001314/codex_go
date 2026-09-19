package tui

import (
	"fmt"
	"strconv"
	"strings"
)

// ThemeAccentColor resolves the shared accent colour for active and selected
// controls (Rust #46504 `style::accent_style`): a custom `.tmTheme` with the
// theme's name wins over the bundled asset, matching the highlighting
// precedence.
func ThemeAccentColor(themeID string) string {
	if accent := customThemeAccent(themeID, DefaultThemeDir()); accent != "" {
		return accent
	}
	return BundledThemeAccent(themeID)
}

// ThemeAccentSGR renders the theme's accent as a bold SGR prefix for terminals
// that can show it: truecolor keeps the theme's rgb colour and 256-colour
// terminals use the indexed colour (Rust #46504 keeps the existing fallback at
// lower colour depths, so those levels return "").
func ThemeAccentSGR(themeID string, level StdoutColorLevel) string {
	if level != ColorTrue && level != ColorANSI256 {
		return ""
	}
	r, g, b, ok := ParseThemeAccentHex(ThemeAccentColor(themeID))
	if !ok {
		return ""
	}
	if level == ColorANSI256 {
		return fmt.Sprintf("\x1b[1;38;5;%dm", ClosestANSI256(RGB{R: r, G: g, B: b}))
	}
	return fmt.Sprintf("\x1b[1;38;2;%d;%d;%dm", r, g, b)
}

// ParseThemeAccentHex parses a "#rrggbb" accent colour.
func ParseThemeAccentHex(value string) (uint8, uint8, uint8, bool) {
	value = strings.TrimSpace(value)
	if len(value) != 7 || !strings.HasPrefix(value, "#") {
		return 0, 0, 0, false
	}
	parsed, err := strconv.ParseUint(value[1:], 16, 32)
	if err != nil {
		return 0, 0, 0, false
	}
	return uint8(parsed >> 16), uint8(parsed >> 8), uint8(parsed), true
}
