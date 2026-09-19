package tui

// Theme-provided diff backgrounds.
//
// Rust parity: tui/src/render/highlight.rs `diff_scope_backgrounds` plus the
// terminal-default sentinel handled by tui/src/diff_render.rs (#46504). A theme
// can either tint a diff scope with a colour or declare the terminal default
// (alpha 1, written `#00000001` by the bundled dali/davinci themes) to disable
// that scope's fill entirely.

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

type diffScopeBackground struct {
	// rgb is the theme colour ("#rrggbb") when the theme declares a real fill.
	rgb string
	// present reports that the theme declares a background for the scope.
	present bool
	// terminalDefault reports Rust's explicit "no fill" marker.
	terminalDefault bool
}

var diffScopeBackgroundCache sync.Map

// themeDiffScopeBackground resolves the background a theme declares for a
// TextMate scope. Custom `.tmTheme` files win over the bundled assets, and
// chroma-mapped themes fall back to the style's token background.
func themeDiffScopeBackground(themeID string, scope string) diffScopeBackground {
	id := strings.ToLower(strings.TrimSpace(themeID))
	if id == "" || strings.TrimSpace(scope) == "" {
		return diffScopeBackground{}
	}
	cacheKey := id + "\x00" + scope
	if cached, ok := diffScopeBackgroundCache.Load(cacheKey); ok {
		return cached.(diffScopeBackground)
	}
	result := diffScopeBackground{}
	if theme := loadCustomTheme(id); theme != nil {
		result = theme.scopeBackground(scope)
	}
	if !result.present {
		if theme := loadBundledTheme(id); theme != nil {
			result = theme.scopeBackground(scope)
		}
	}
	// Themes Go resolves through chroma's built-in style names have no TextMate
	// source here, so their scope backgrounds are unknowable: chroma's
	// `GenericInserted` background is a generic port colour rather than the
	// theme's diff fill, and using it would tint diffs Rust leaves on the
	// fallback palette. Only parsed `.tmTheme` documents contribute a fill.
	diffScopeBackgroundCache.Store(cacheKey, result)
	return result
}

// loadCustomTheme parses a user `.tmTheme` for lookups that need the raw scope
// settings (the chroma style loses the alpha sentinel).
func loadCustomTheme(name string) *tmTheme {
	id := strings.ToLower(strings.TrimSpace(name))
	if id == "" {
		return nil
	}
	if cached, ok := customThemeParses.Load(id); ok {
		if theme, ok := cached.(*tmTheme); ok {
			return theme
		}
		return nil
	}
	data, err := os.ReadFile(filepath.Join(DefaultThemeDir(), id+".tmTheme"))
	if err != nil {
		customThemeParses.Store(id, (*tmTheme)(nil))
		return nil
	}
	theme, err := parseTMTheme(data)
	if err != nil {
		customThemeParses.Store(id, (*tmTheme)(nil))
		return nil
	}
	customThemeParses.Store(id, theme)
	return theme
}

var customThemeParses sync.Map

// scopeBackground returns the theme's background for a TextMate scope name,
// preserving Rust's terminal-default marker.
func (t *tmTheme) scopeBackground(scope string) diffScopeBackground {
	if t == nil {
		return diffScopeBackground{}
	}
	for _, scoped := range t.Scopes {
		for _, selector := range strings.Split(scoped.Scope, ",") {
			if !strings.EqualFold(strings.TrimSpace(selector), scope) {
				continue
			}
			raw := strings.TrimSpace(scoped.Settings.Background)
			if raw == "" {
				continue
			}
			if isTerminalDefaultBackground(raw) {
				return diffScopeBackground{present: true, terminalDefault: true}
			}
			if colour := tmThemeColour(raw); colour != "" {
				return diffScopeBackground{rgb: colour, present: true}
			}
		}
	}
	return diffScopeBackground{}
}

// isTerminalDefaultBackground mirrors Rust's ANSI_ALPHA_DEFAULT check: an
// alpha of 1 marks the terminal default rather than a colour.
func isTerminalDefaultBackground(raw string) bool {
	value := strings.TrimPrefix(strings.TrimSpace(raw), "#")
	return len(value) == 8 && strings.EqualFold(value[6:], "01")
}

// diffScopeNames lists the scopes Rust queries, preferring the `markup.*`
// convention used by most VS Code themes over the older `diff.*` form.
func diffScopeNames(lineType DiffLineType) []string {
	switch lineType {
	case DiffLineInsert:
		return []string{"markup.inserted", "diff.inserted"}
	case DiffLineDelete:
		return []string{"markup.deleted", "diff.deleted"}
	default:
		return nil
	}
}

// resolveDiffLineBackground mirrors Rust `resolve_diff_backgrounds_for`: the
// hardcoded palette is the baseline, a theme scope background overrides it at
// rich colour levels only, and the terminal-default marker clears the fill.
func resolveDiffLineBackground(theme string, lineType DiffLineType, light bool, level StdoutColorLevel) string {
	scopes := diffScopeNames(lineType)
	if len(scopes) == 0 || (level != ColorTrue && level != ColorANSI256) {
		return diffFallbackBgSGR(lineType, light, level)
	}
	for _, scope := range scopes {
		background := themeDiffScopeBackground(theme, scope)
		switch {
		case background.terminalDefault:
			return ""
		case background.rgb != "":
			return backgroundColorSGR(background.rgb, level)
		}
	}
	return diffFallbackBgSGR(lineType, light, level)
}

// backgroundColorSGR renders a theme background as an SGR prefix at the given
// colour depth.
func backgroundColorSGR(colour string, level StdoutColorLevel) string {
	r, g, b, ok := ParseThemeAccentHex(colour)
	if !ok {
		return ""
	}
	best := BestColorForLevel(RGB{R: r, G: g, B: b}, level)
	if best.Index != nil {
		return "\x1b[48;5;" + strconv.Itoa(int(*best.Index)) + "m"
	}
	if best.RGB != nil {
		return "\x1b[48;2;" + strconv.Itoa(int(best.RGB.R)) + ";" + strconv.Itoa(int(best.RGB.G)) + ";" + strconv.Itoa(int(best.RGB.B)) + "m"
	}
	return ""
}
