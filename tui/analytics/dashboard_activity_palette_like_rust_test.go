package analytics

import (
	"strings"
	"testing"
	"time"

	codextui "codex_go/tui"
	"codex_go/tui/chatwidget"
)

// Rust #46897: analytics rows are measured in terminal cells, so SGR sequences
// produced by the activity palette never count toward the card width.
func TestAnalyticsDisplayWidthIgnoresANSILikeRust(t *testing.T) {
	cases := []struct {
		text string
		want int
	}{
		{"\x1b[38;2;95;175;255m\u25a0\x1b[0m \u25a0", 3},
		{"\x1b[38;5;45m\u25a0\x1b[0m", 1},
		{"\u4e16\u754c", 4},
		{"plain", 5},
	}
	for _, testCase := range cases {
		if got := analyticsDisplayWidth(testCase.text); got != testCase.want {
			t.Fatalf("analyticsDisplayWidth(%q) = %d, want %d", testCase.text, got, testCase.want)
		}
	}
	if got := analyticsTruncate("\x1b[38;2;95;175;255mabcdef\x1b[0m", 4); analyticsDisplayWidth(got) != 4 {
		t.Fatalf("styled truncate width = %d (%q), want 4 cells", analyticsDisplayWidth(got), got)
	}
	if strings.Contains(analyticsTruncate("\x1b[38;2;95;175;255mabcdef\x1b[0m", 4), "\x1b[38;2;95;175;255ma") == false {
		t.Fatal("styled truncate dropped the palette prefix")
	}
}

// Rust #46897: the Summary token activity calendar honors the effective color
// level, and the dashboard measures the resulting styled rows correctly at
// every supported level.
func TestAnalyticsSummaryActivityPaletteHonorsColorLevelLikeRust(t *testing.T) {
	restoreColors := codextui.SetDefaultTerminalColorsForTest(&codextui.DefaultColors{
		FG: codextui.RGBColor{R: 240, G: 240, B: 240},
		BG: codextui.RGBColor{},
	})
	defer restoreColors()

	cases := []struct {
		level      codextui.StdoutColorLevel
		wantPrefix string
	}{
		{codextui.ColorUnknown, ""},
		{codextui.ColorANSI16, ""},
		{codextui.ColorANSI256, "\x1b[38;5;"},
		{codextui.ColorTrue, "\x1b[38;2;"},
	}
	endDate := time.Date(2026, 9, 9, 0, 0, 0, 0, time.UTC)
	for _, testCase := range cases {
		restoreLevel := codextui.SetStdoutColorLevelForTest(testCase.level)
		lines := AnalyticsSummaryLines(AnalyticsSummaryOptions{
			Profile: summaryTestProfile(),
			Ready:   true,
			Zoomed:  true,
			View:    0,
			EndDate: endDate,
			Width:   80,
			Theme:   "ada",
		})
		restoreLevel()
		joined := strings.Join(lines, "\n")
		if testCase.wantPrefix == "" {
			if strings.Contains(joined, "\x1b[38;") {
				t.Fatalf("color level %d emitted a color ramp:\n%q", testCase.level, joined)
			}
		} else if !strings.Contains(joined, testCase.wantPrefix) {
			t.Fatalf("color level %d did not use %q:\n%q", testCase.level, testCase.wantPrefix, joined)
		}
		for _, line := range lines {
			if width := analyticsDisplayWidth(line); width > 80 {
				t.Fatalf("color level %d line width = %d, want <= 80: %q", testCase.level, width, line)
			}
		}
	}
}

// Rust #46897: a narrow analytics dashboard still lays the styled chart out
// inside the card, so the palette cannot break wrapping or clipping.
func TestAnalyticsDashboardNarrowWidthKeepsStyledRowsInsideCardLikeRust(t *testing.T) {
	restoreColors := codextui.SetDefaultTerminalColorsForTest(&codextui.DefaultColors{
		FG: codextui.RGBColor{R: 240, G: 240, B: 240},
		BG: codextui.RGBColor{},
	})
	defer restoreColors()
	restoreLevel := codextui.SetStdoutColorLevelForTest(codextui.ColorTrue)
	defer restoreLevel()

	for _, width := range []int{32, 36, 39} {
		panel := AnalyticsPanelLines(AnalyticsPanelOptions{
			Section:      AnalyticsSectionSummary,
			State:        AnalyticsSectionState{Group: 0},
			Profile:      summaryTestProfile(),
			ProfileReady: true,
			EndDate:      time.Date(2026, 9, 9, 0, 0, 0, 0, time.UTC),
			Width:        width,
			Theme:        "ada",
		})
		lines := panel.Lines
		if len(lines) == 0 {
			t.Fatalf("width %d produced no dashboard lines", width)
		}
		styled := 0
		for _, line := range lines {
			if strings.Contains(line, "\x1b[38;") {
				styled++
			}
			if got := analyticsDisplayWidth(line); got > width {
				t.Fatalf("width %d: rendered line measures %d cells: %q", width, got, line)
			}
		}
		if styled == 0 {
			t.Fatalf("width %d: the rendered dashboard never exercised the styled activity chart", width)
		}
	}
}

// Rust #46897: the calendar palette is the same object the /usage chart uses,
// so the dashboard and the usage view cannot drift apart.
func TestAnalyticsSummaryUsesSharedActivityPaletteLikeRust(t *testing.T) {
	restoreColors := codextui.SetDefaultTerminalColorsForTest(&codextui.DefaultColors{
		FG: codextui.RGBColor{R: 240, G: 240, B: 240},
		BG: codextui.RGBColor{},
	})
	defer restoreColors()
	restoreLevel := codextui.SetStdoutColorLevelForTest(codextui.ColorTrue)
	defer restoreLevel()

	palette := chatwidget.CurrentTokenActivityPalette("ada")
	buckets := []chatwidget.TokenActivityDailyBucket{{StartDate: "2026-09-09", Tokens: 42}}
	today := time.Date(2026, 9, 9, 0, 0, 0, 0, time.UTC)
	want := strings.Join(chatwidget.TokenActivityCalendarLinesPalette(chatwidget.TokenActivityDaily, buckets, today, 60, palette), "\n")
	lines := AnalyticsSummaryLines(AnalyticsSummaryOptions{
		Profile: summaryTestProfile(),
		Ready:   true,
		Zoomed:  true,
		View:    0,
		EndDate: today,
		Width:   60,
		Theme:   "ada",
	})
	joined := strings.Join(lines, "\n")
	if want == "" || !strings.Contains(joined, strings.Split(want, "\n")[0]) {
		t.Fatalf("summary chart missing the shared palette rows:\nwant:\n%s\ngot:\n%s", want, joined)
	}
}
