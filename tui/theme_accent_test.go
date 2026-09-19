
package tui

import "testing"

// Mirrors Rust #46504 style::tests::theme_accents_respect_terminal_color_depth:
// the theme accent keeps its rgb colour on truecolor terminals, uses the indexed
// colour on 256-colour terminals, and leaves the fallback in place below that.
func TestThemeAccentSGRRespectsColorDepthLikeRust(t *testing.T) {
	if got, want := ThemeAccentSGR("ada", ColorTrue), "\x1b[1;38;2;95;175;255m"; got != want {
		t.Fatalf("truecolor accent = %q, want %q", got, want)
	}
	if got, want := ThemeAccentSGR("ada", ColorANSI256), "\x1b[1;38;5;75m"; got != want {
		t.Fatalf("256-colour accent = %q, want %q", got, want)
	}
	for _, level := range []StdoutColorLevel{ColorANSI16, ColorUnknown} {
		if got := ThemeAccentSGR("ada", level); got != "" {
			t.Fatalf("level %v accent = %q, want the fallback", level, got)
		}
	}
	if got := ThemeAccentSGR("catppuccin-mocha", ColorTrue); got != "" {
		t.Fatalf("theme without a codex.accent = %q, want the fallback", got)
	}
}
