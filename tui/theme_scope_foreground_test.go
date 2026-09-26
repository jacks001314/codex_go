package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeScopeForegroundTheme(t *testing.T, name string, scopes [][2]string) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	t.Setenv("GCODE_HOME", home)
	themeDir := DefaultThemeDir()
	if err := os.MkdirAll(themeDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(%s) error = %v", themeDir, err)
	}
	var body strings.Builder
	body.WriteString("<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n")
	body.WriteString("<!DOCTYPE plist PUBLIC \"-//Apple//DTD PLIST 1.0//EN\" \"http://www.apple.com/DTDs/PropertyList-1.0.dtd\">\n")
	body.WriteString("<plist version=\"1.0\">\n<dict>\n  <key>name</key><string>" + name + "</string>\n")
	body.WriteString("  <key>settings</key>\n  <array>\n")
	body.WriteString("    <dict><key>settings</key><dict><key>foreground</key><string>#D0D0D0</string></dict></dict>\n")
	for _, scope := range scopes {
		body.WriteString("    <dict><key>scope</key><string>" + scope[0] + "</string>\n")
		body.WriteString("      <key>settings</key><dict><key>foreground</key><string>" + scope[1] + "</string></dict></dict>\n")
	}
	body.WriteString("  </array>\n</dict>\n</plist>\n")
	if err := os.WriteFile(filepath.Join(themeDir, name+".tmTheme"), []byte(body.String()), 0o600); err != nil {
		t.Fatalf("WriteFile(theme) error = %v", err)
	}
	return name
}

func isRGB(colour TerminalColor, r uint8, g uint8, b uint8) bool {
	return colour.RGB != nil && colour.Index == nil && *colour.RGB == (RGB{R: r, G: g, B: b})
}

// Mirrors Rust `foreground_style_for_scopes_with_theme`: the first scope the
// theme styles wins, a scope the theme does not style yields no colour, and a
// dotted-prefix theme selector matches a more specific scope.
func TestThemeScopeForegroundLikeRust(t *testing.T) {
	name := writeScopeForegroundTheme(t, "mermaid-scope-foreground-test", [][2]string{
		{"comment", "#123456"},
		{"entity.name.type", "#abcdef"},
		{"entity.name", "#0f0f0f"},
	})

	if got, ok := themeScopeForeground(name, "entity.name.type", "support.type", "variable"); !ok || !isRGB(got, 0xAB, 0xCD, 0xEF) {
		t.Fatalf("node colour = %#v ok=%v, want #abcdef", got, ok)
	}
	if got, ok := themeScopeForeground(name, "comment"); !ok || !isRGB(got, 0x12, 0x34, 0x56) {
		t.Fatalf("edge colour = %#v ok=%v, want #123456", got, ok)
	}
	// A scope the theme does not style (directly or through a prefix) is absent,
	// so the caller keeps Rust's cyan/dim fallback.
	if got, ok := themeScopeForeground(name, "keyword.control"); ok {
		t.Fatalf("unstyled scope colour = %#v, want none", got)
	}
	// A theme selector that is a dotted prefix covers the more specific scope.
	if got, ok := themeScopeForeground(name, "entity.name.function"); !ok || !isRGB(got, 0x0F, 0x0F, 0x0F) {
		t.Fatalf("prefix selector colour = %#v ok=%v, want #0f0f0f", got, ok)
	}
	if got, ok := themeScopeForeground("", "comment"); ok {
		t.Fatalf("empty theme colour = %#v, want none", got)
	}
	if got := ThemeScopeForegroundSGR(name, "comment"); got != "\x1b[38;2;18;52;86m" {
		t.Fatalf("comment SGR = %q, want \\x1b[38;2;18;52;86m", got)
	}
	if got := ThemeScopeForegroundSGR(name, "keyword.control"); got != "" {
		t.Fatalf("unstyled scope SGR = %q, want empty", got)
	}
}

// Rust's `convert_syntect_color` decodes bat's alpha encodings: alpha 0x00 stores
// an ANSI palette index in red, alpha 0x01 means the terminal default (no colour).
func TestThemeScopeForegroundDecodesAnsiAlpha(t *testing.T) {
	indexed := writeScopeForegroundTheme(t, "mermaid-scope-index-test", [][2]string{
		{"comment", "#05050500"},
	})
	if got, ok := themeScopeForeground(indexed, "comment"); !ok || got.Index == nil || *got.Index != 0x05 || got.RGB != nil {
		t.Fatalf("indexed colour = %#v ok=%v, want palette index 5", got, ok)
	}
	if got := ThemeScopeForegroundSGR(indexed, "comment"); got != "\x1b[38;5;5m" {
		t.Fatalf("indexed SGR = %q, want \\x1b[38;5;5m", got)
	}

	terminalDefault := writeScopeForegroundTheme(t, "mermaid-scope-default-test", [][2]string{
		{"comment", "#05050501"},
	})
	if got, ok := themeScopeForeground(terminalDefault, "comment"); ok {
		t.Fatalf("terminal-default colour = %#v, want none", got)
	}
}

// A chroma-mapped theme (no TextMate source in Go) resolves the scope through the
// style's own token colour rather than the caller's fallback.
func TestThemeScopeForegroundUsesChromaThemeColour(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	t.Setenv("GCODE_HOME", home)
	colour, ok := themeScopeForeground("catppuccin-mocha", "comment")
	if !ok || colour.RGB == nil {
		t.Fatalf("catppuccin-mocha comment colour = %#v ok=%v, want a concrete rgb", colour, ok)
	}
}
