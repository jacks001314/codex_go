package tui

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/alecthomas/chroma/v2"
	chromastyles "github.com/alecthomas/chroma/v2/styles"
)

// minimalTMThemeContent mirrors Rust's write_minimal_tmtheme: a plist with a
// name and one unscoped settings block, which syntect (and our parser) accept.
const minimalTMThemeContent = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>name</key><string>Test</string>
<key>settings</key><array><dict>
<key>settings</key><dict>
<key>foreground</key><string>#FFFFFF</string>
<key>background</key><string>#000000</string>
</dict></dict></array>
</dict></plist>`

// scopedTMThemeContent adds scoped entries so the Chroma mapping can be checked.
const scopedTMThemeContent = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>name</key><string>Scoped</string>
<key>settings</key><array>
<dict>
<key>settings</key><dict>
<key>foreground</key><string>#d4d4d4</string>
<key>background</key><string>#1e1e1e</string>
</dict>
</dict>
<dict>
<key>scope</key><string>comment</string>
<key>settings</key><dict>
<key>foreground</key><string>#6a9955</string>
<key>fontStyle</key><string>italic</string>
</dict>
</dict>
<dict>
<key>scope</key><string>keyword, storage</string>
<key>settings</key><dict>
<key>foreground</key><string>#ff79c6</string>
<key>fontStyle</key><string>bold</string>
</dict>
</dict>
</array>
</dict></plist>`

// TestParseTMThemeAcceptsValidAndRejectsInvalid covers the loader's acceptance
// rule (syntect's ThemeSet::get_theme): a plist with a settings array parses, and
// anything else is rejected so the startup warning can surface it.
func TestParseTMThemeAcceptsValidAndRejectsInvalid(t *testing.T) {
	theme, err := parseTMTheme([]byte(minimalTMThemeContent))
	if err != nil {
		t.Fatalf("parse minimal theme: %v", err)
	}
	if theme.Name != "Test" || theme.Settings.Foreground != "#FFFFFF" || theme.Settings.Background != "#000000" {
		t.Fatalf("minimal theme = %#v", theme)
	}

	theme, err = parseTMTheme([]byte(scopedTMThemeContent))
	if err != nil {
		t.Fatalf("parse scoped theme: %v", err)
	}
	if len(theme.Scopes) != 2 || theme.Scopes[1].Scope != "keyword, storage" {
		t.Fatalf("scoped theme = %#v", theme)
	}

	for _, invalid := range []string{"placeholder", "<plist/>", `<plist version="1.0"><dict><key>name</key><string>x</string></dict></plist>`} {
		if _, err := parseTMTheme([]byte(invalid)); err == nil {
			t.Fatalf("parseTMTheme(%q) succeeded, want an error", invalid)
		}
	}
}

// TestCustomThemeChromaStyleAppliesLikeRust covers the loading half: a custom
// `.tmTheme` resolves to a registered Chroma style whose mapped token colors come
// from the theme instead of the bundled fallback.
func TestCustomThemeChromaStyleAppliesLikeRust(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	t.Setenv("GCODE_HOME", "")
	themesDir := filepath.Join(home, "themes")
	if err := os.MkdirAll(themesDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(themesDir, "tmtheme-scoped-case.tmTheme"), []byte(scopedTMThemeContent), 0o600); err != nil {
		t.Fatal(err)
	}

	styleName := ChromaThemeForCodexTheme("tmtheme-scoped-case")
	if styleName != "codex-tmtheme-tmtheme-scoped-case" {
		t.Fatalf("custom style = %q", styleName)
	}
	style := chromastyles.Get(styleName)
	if style == nil || style.Name != styleName {
		t.Fatalf("registered style = %#v", style)
	}
	if colour := style.Get(chroma.Keyword).Colour.String(); colour != "#ff79c6" {
		t.Fatalf("keyword colour = %q, want the theme's colour", colour)
	}
	if colour := style.Get(chroma.Comment).Colour.String(); colour != "#6a9955" {
		t.Fatalf("comment colour = %q, want the theme's colour", colour)
	}

	// A bundled name still resolves through the bundled mapping.
	if got := ChromaThemeForCodexTheme("dracula"); got != "dracula" {
		t.Fatalf("bundled style = %q", got)
	}
	// An unknown name with no file falls back to the default style.
	if got := ChromaThemeForCodexTheme("tmtheme-absent-case"); got != "catppuccin-mocha" {
		t.Fatalf("default style = %q", got)
	}
}

// TestDiscoverCustomThemePathsExcludesInvalidThemes covers Rust's
// list_available_themes: a `.tmTheme` the loader cannot parse is not offered.
func TestDiscoverCustomThemePathsExcludesInvalidThemes(t *testing.T) {
	themesDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(themesDir, "valid-custom.tmTheme"), []byte(minimalTMThemeContent), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(themesDir, "broken-custom.tmTheme"), []byte("not a plist"), 0o600); err != nil {
		t.Fatal(err)
	}
	paths := DiscoverCustomThemePaths(themesDir)
	if len(paths) != 1 || filepath.Base(paths[0]) != "valid-custom.tmTheme" {
		t.Fatalf("custom paths = %#v", paths)
	}
}
