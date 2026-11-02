package analytics

import (
	"strings"
	"testing"
	"time"

	codextui "codex_go/tui"
	"codex_go/tui/chatwidget"
)

func activityPaletteTestColors(t *testing.T, level codextui.StdoutColorLevel) func() {
	t.Helper()
	restoreColors := codextui.SetDefaultTerminalColorsForTest(&codextui.DefaultColors{
		FG: codextui.RGBColor{R: 240, G: 240, B: 240},
		BG: codextui.RGBColor{},
	})
	restoreLevel := codextui.SetStdoutColorLevelForTest(level)
	return func() {
		restoreLevel()
		restoreColors()
	}
}

// Rust #46897: the token activity palette is built from the *effective*
// terminal color level, so a terminal that is promoted to truecolor gets the
// five-step theme ramp instead of the low-color glyph fallback.
func TestActivityChartPaletteUsesEffectiveTerminalColorLevelLikeRust(t *testing.T) {
	defer activityPaletteTestColors(t, codextui.ColorTrue)()

	palette := chatwidget.CurrentTokenActivityPalette("ada")
	if !palette.UsesColor() {
		t.Fatal("the palette must use color at the effective truecolor level")
	}
	// Go resolves the ambient activity accent from the theme's `codex.accent`
	// scope (#5FAFFF for Ada), so the ramp is that accent blended against the
	// terminal background with the Rust palette's alphas; level 0 blends the
	// terminal foreground so empty cells stay visible.
	for level, want := range []string{
		"\x1b[38;2;33;33;33m",
		"\x1b[38;2;20;38;56m",
		"\x1b[38;2;39;73;107m",
		"\x1b[38;2;64;119;173m",
		"\x1b[38;2;95;175;255m",
	} {
		if got := palette.Glyph(chatwidget.TokenActivityDaily, level); got != "\u25a0" {
			t.Fatalf("level %d glyph = %q, want the color-carried square", level, got)
		}
		if got := palette.StyleForLevel(level); got != want {
			t.Fatalf("level %d style = %q, want %q", level, got, want)
		}
	}

	today := time.Date(2026, 5, 29, 0, 0, 0, 0, time.UTC)
	buckets := []chatwidget.TokenActivityDailyBucket{{StartDate: "2026-05-25", Tokens: 9}}
	state := chatwidget.NewTokenActivityLoadedState(chatwidget.TokenActivityResponse{DailyUsageBuckets: &buckets}, today)
	lines := strings.Join(chatwidget.TokenActivityLinesPalette(chatwidget.TokenActivityDaily, state, 80, palette), "\n")
	if !strings.Contains(lines, "\x1b[38;2;95;175;255m\u25a0") {
		t.Fatalf("chart does not render the top activity color:\n%q", lines)
	}
}

// Rust #46897 fallback: a low-color terminal keeps the hollow/filled glyph pair
// and the chart never emits a color ramp.
func TestActivityChartPaletteFallsBackAtLowColorLikeRust(t *testing.T) {
	defer activityPaletteTestColors(t, codextui.ColorANSI16)()

	palette := chatwidget.CurrentTokenActivityPalette("ada")
	if palette.UsesColor() {
		t.Fatal("an ANSI-16 terminal must not use the color ramp")
	}
	if got := palette.Glyph(chatwidget.TokenActivityDaily, 0); got != "\u25a1" {
		t.Fatalf("empty cell glyph = %q, want the hollow square", got)
	}
	if got := palette.StyleForLevel(4); got != "" {
		t.Fatalf("low-color level style = %q, want none", got)
	}

	today := time.Date(2026, 5, 29, 0, 0, 0, 0, time.UTC)
	buckets := []chatwidget.TokenActivityDailyBucket{{StartDate: "2026-05-25", Tokens: 9}}
	state := chatwidget.NewTokenActivityLoadedState(chatwidget.TokenActivityResponse{DailyUsageBuckets: &buckets}, today)
	lines := strings.Join(chatwidget.TokenActivityLinesPalette(chatwidget.TokenActivityDaily, state, 80, palette), "\n")
	if strings.Contains(lines, "\x1b[38;") {
		t.Fatalf("low-color chart emitted a color ramp:\n%q", lines)
	}
	if !strings.Contains(lines, "\u25a1") || !strings.Contains(lines, "\u25a0") {
		t.Fatalf("low-color chart lost its glyph pair:\n%q", lines)
	}
}
