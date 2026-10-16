package tea

import (
	"strings"
	"testing"

	codextui "codex_go/tui"
	bottompane "codex_go/tui/bottom_pane"
)

// Rust parity: codex-rs/tui/src/bottom_pane/status_line_style.rs (#53440,
// "Preserve status line accents on 16-color terminals"). The Rust commit keeps a
// themed span's RGB foreground only while the terminal can display it
// (effective_stdout_color_level() == Ansi16 is the fallback case), and its
// regression test distinguishes FORCE_COLOR=1 (ANSI accents survive) from
// FORCE_COLOR=0 (no color at all).
func TestStatusLineAccentKeepsAccentOn16ColorTerminalsLikeRust(t *testing.T) {
	const theme = "catppuccin-mocha"
	themeColor := statusLineAccentSGRForLevel(theme, bottompane.StatusLineAccentModel, codextui.ColorTrue)
	if !strings.Contains(themeColor, "\x1b[38;2;") {
		t.Fatalf("truecolor model accent = %q, want a softened RGB foreground", themeColor)
	}
	if got := statusLineAccentSGRForLevel(theme, bottompane.StatusLineAccentModel, codextui.ColorANSI256); got != themeColor {
		t.Fatalf("256-color model accent = %q, want the theme foreground %q (Rust keeps Rgb at Ansi256)", got, themeColor)
	}
	// A 16-color terminal cannot display the RGB foreground, so the item keeps its
	// ANSI fallback accent (cyan for the model, Rust's fallback_style()).
	if got, want := statusLineAccentSGRForLevel(theme, bottompane.StatusLineAccentModel, codextui.ColorANSI16), "\x1b[36m"; got != want {
		t.Fatalf("16-color model accent = %q, want %q", got, want)
	}
	if got, want := statusLineAccentSGRForLevel(theme, bottompane.StatusLineAccentPath, codextui.ColorANSI16), "\x1b[32m"; got != want {
		t.Fatalf("16-color path accent = %q, want %q", got, want)
	}
	// A terminal that reports no color support resolves the themed foreground to the
	// terminal default (Rust: readable_color_on -> Color::Reset, the FORCE_COLOR=0 case).
	if got, want := statusLineAccentSGRForLevel(theme, bottompane.StatusLineAccentModel, codextui.ColorUnknown), statusLineDefaultForegroundSGR; got != want {
		t.Fatalf("colorless model accent = %q, want %q", got, want)
	}
	// An accent the theme contributes no color for still defers to the fallback
	// palette, as before.
	if got := statusLineAccentSGRForLevel(theme, bottompane.StatusLineAccentNone, codextui.ColorANSI16); got != "" {
		t.Fatalf("unthemed accent = %q, want an empty SGR", got)
	}
}

// At FORCE_COLOR=1 Rust renders the model/path/usage segments with Cyan/Green/Green
// accents in the buffer; the same accents must survive a 16-color terminal here.
func TestStatusLineRenderAt16ColorsKeepsAnsiAccentsLikeRust(t *testing.T) {
	line, ok := bottompane.StatusLineFromSegments([]bottompane.StatusLineSegment{
		{Item: bottompane.StatusLineModelName, Text: "gpt-5"},
		{Item: bottompane.StatusLineCurrentDir, Text: "~/code"},
		{Item: bottompane.StatusLineUsedTokens, Text: "12k tokens"},
		{Item: bottompane.StatusLineGitBranch, Text: "main"},
	}, true)
	if !ok {
		t.Fatal("status line was not rendered")
	}
	styled := line.RenderStyled("", func(accent bottompane.StatusLineAccent) string {
		return statusLineAccentSGRForLevel("catppuccin-mocha", accent, codextui.ColorANSI16)
	}, false)
	for _, want := range []string{
		"\x1b[36mgpt-5\x1b[0m",
		"\x1b[32m~/code\x1b[0m",
		"\x1b[32m12k tokens\x1b[0m",
		"\x1b[35mmain\x1b[0m",
	} {
		if !strings.Contains(styled, want) {
			t.Fatalf("16-color status line = %q, missing %q", styled, want)
		}
	}
	if strings.Contains(styled, "38;2;") || strings.Contains(styled, "38;5;") {
		t.Fatalf("16-color status line = %q, want no extended color sequences", styled)
	}
}

// FORCE_COLOR=0 (Rust's level-0 case): every themed span renders without a color
// of its own instead of keeping an unshowable foreground.
func TestStatusLineRenderWithoutColorSupportDropsAccentsLikeRust(t *testing.T) {
	line, ok := bottompane.StatusLineFromSegments([]bottompane.StatusLineSegment{
		{Item: bottompane.StatusLineModelName, Text: "gpt-5"},
		{Item: bottompane.StatusLineCurrentDir, Text: "~/code"},
	}, true)
	if !ok {
		t.Fatal("status line was not rendered")
	}
	styled := line.RenderStyled("", func(accent bottompane.StatusLineAccent) string {
		return statusLineAccentSGRForLevel("catppuccin-mocha", accent, codextui.ColorUnknown)
	}, false)
	if !strings.Contains(styled, "\x1b[39mgpt-5\x1b[0m") {
		t.Fatalf("colorless status line = %q, want the model span on the default foreground", styled)
	}
	for _, unwanted := range []string{"38;2;", "38;5;", "\x1b[36m", "\x1b[32m", "\x1b[35m"} {
		if strings.Contains(styled, unwanted) {
			t.Fatalf("colorless status line = %q, want no accent color (%q)", styled, unwanted)
		}
	}
}
