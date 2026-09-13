package tui

import (
	"os"
	"path/filepath"
	"strings"
)

// Rust parity: codex-rs/tui/src/render/highlight.rs validate_theme_name. The TUI
// validates the configured `tui.theme` at startup and reports a warning when the
// name is neither a bundled theme nor a custom theme file.
func ThemeStartupWarning(name string, codexHome string) string {
	theme := strings.TrimSpace(name)
	if theme == "" {
		return ""
	}
	display := themePathDisplay(theme, codexHome)
	if BundledThemeName(theme) {
		return ""
	}
	if path := customThemePath(theme, codexHome); path != "" {
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			// A custom theme must parse: an unreadable or invalid `.tmTheme`
			// surfaces a startup warning so users can diagnose it.
			if _, err := LoadCustomTheme(theme, codexHome); err == nil {
				return ""
			}
			return "Custom theme \"" + theme + "\" at " + display +
				" could not be loaded (invalid .tmTheme format). Falling back to the default theme."
		}
	}
	return "Theme \"" + theme + "\" not found. Using the default theme. " +
		"To use a custom theme, place a .tmTheme file at " + display + "."
}

// BundledThemeName reports whether the theme name is one of the bundled themes
// (Rust parse_theme_name).
func BundledThemeName(name string) bool {
	theme := strings.ToLower(strings.TrimSpace(name))
	if theme == "" {
		return false
	}
	for _, id := range builtinThemeIDs {
		if id == theme {
			return true
		}
	}
	return false
}

func customThemePath(name string, codexHome string) string {
	home := strings.TrimSpace(codexHome)
	if home == "" {
		return ""
	}
	return filepath.Join(home, "themes", name+".tmTheme")
}

// themePathDisplay mirrors Rust's custom_theme_path_display: the resolved path,
// or the `$CODEX_HOME` form when no home is known.
func themePathDisplay(name string, codexHome string) string {
	if path := customThemePath(name, codexHome); path != "" {
		return path
	}
	return filepath.Join("$CODEX_HOME", "themes", name+".tmTheme")
}
