package analytics

import (
	"strings"
	"testing"
	"time"

	"github.com/mattn/go-runewidth"
)

func plotTestValue(key string, value float64) AccountAnalyticsValue {
	return AccountAnalyticsValue{Key: key, Label: key, Value: value}
}

func plotTestDate(t *testing.T, date string) time.Time {
	t.Helper()
	parsed, err := time.Parse("2006-01-02", date)
	if err != nil {
		t.Fatalf("parse date %q: %v", date, err)
	}
	return parsed
}

func plotTestDay(t *testing.T, date string, total float64, values ...AccountAnalyticsValue) *AccountAnalyticsDay {
	t.Helper()
	return &AccountAnalyticsDay{Date: plotTestDate(t, date), Total: total, Values: values}
}

func plotTestDays(days ...*AccountAnalyticsDay) []AnalyticsPlotDay {
	slots := make([]AnalyticsPlotDay, 0, len(days))
	for _, day := range days {
		if day == nil {
			continue
		}
		slots = append(slots, AnalyticsPlotDay{Date: day.Date, Day: day})
	}
	return slots
}

func plotTestLineTexts(chart AnalyticsChart) []string {
	lines := make([]string, 0, len(chart.Lines))
	for _, row := range chart.Lines {
		var builder strings.Builder
		for _, cell := range row {
			builder.WriteRune(cell.Rune)
		}
		lines = append(lines, builder.String())
	}
	return lines
}

func plotTestLineWidth(chart AnalyticsChart, index int) int {
	width := 0
	for _, cell := range chart.Lines[index] {
		width += runewidth.RuneWidth(cell.Rune)
	}
	return width
}

// TestAnalyticsAxisMaxUsesReadableIntervalsLikeRust mirrors Rust's
// numeric_axes_use_readable_intervals (#45763).
func TestAnalyticsAxisMaxUsesReadableIntervalsLikeRust(t *testing.T) {
	cases := []struct {
		peak     float64
		unit     AccountAnalyticsUnit
		expected float64
	}{
		{82, AccountAnalyticsUnitCount, 100},
		{4400, AccountAnalyticsUnitCredits, 5000},
		{4999, AccountAnalyticsUnitCredits, 5000},
		{5001, AccountAnalyticsUnitCredits, 6000},
		{49, AccountAnalyticsUnitCount, 50},
		{1, AccountAnalyticsUnitCount, 2},
		{3, AccountAnalyticsUnitCount, 4},
		{1337, AccountAnalyticsUnitCredits, 1500},
		{0.07, AccountAnalyticsUnitCredits, 0.08},
		{0.000004, AccountAnalyticsUnitCredits, 0.000004},
		{82, AccountAnalyticsUnitRelativeUsage, 100},
		{1, AccountAnalyticsUnitTokens, 2},
	}
	for _, test := range cases {
		if got := AnalyticsAxisMax(test.peak, test.unit); got != test.expected {
			t.Errorf("AnalyticsAxisMax(%v, %v) = %v, want %v", test.peak, test.unit, got, test.expected)
		}
	}
}

// TestAnalyticsPartsKeepTheRemainderAndFullDenominatorLikeRust mirrors Rust's
// stacked_parts_keep_the_remainder_and_full_denominator.
func TestAnalyticsPartsKeepTheRemainderAndFullDenominatorLikeRust(t *testing.T) {
	values := []AccountAnalyticsValue{
		plotTestValue("a", 4),
		plotTestValue("b", 2),
		plotTestValue("c", 1),
		plotTestValue("d", 6),
		plotTestValue("e", -1),
	}
	categories := []AccountAnalyticsValue{values[1], values[0], values[2]}
	if got := AnalyticsParts(values, categories); got != [4]float64{2, 4, 1, 5} {
		t.Fatalf("parts = %v, want [2 4 1 5]", got)
	}
	day := plotTestDay(t, "2026-01-15", 20, values...)
	if got := AnalyticsPeak(plotTestDays(day), categories); got != 20 {
		t.Fatalf("peak = %v, want 20", got)
	}
	refund := plotTestDay(t, "2026-01-15", -1, plotTestValue("a", 4), plotTestValue("b", -5))
	if got := AnalyticsPeak(plotTestDays(refund), categories); got != 9 {
		t.Fatalf("refund peak = %v, want 9", got)
	}
}

// TestAnalyticsPlotSignedDaysUseBothBandsLikeRust mirrors Rust's signed-chart
// snapshots; refunds grow down from the baseline instead of stacking upward.
func TestAnalyticsPlotSignedDaysUseBothBandsLikeRust(t *testing.T) {
	days := plotTestDays(
		plotTestDay(t, "2026-01-13", 8, plotTestValue("a", 10), plotTestValue("b", -2)),
		plotTestDay(t, "2026-01-14", -3, plotTestValue("a", 1), plotTestValue("b", -4)),
		plotTestDay(t, "2026-01-15", 0, plotTestValue("a", 3), plotTestValue("b", -3)),
	)
	chart := AnalyticsPlot(days, []AccountAnalyticsValue{plotTestValue("a", 0), plotTestValue("b", 0)}, 1, 44, 4, AccountAnalyticsUnitCredits)
	if chart.Bands != 2 {
		t.Fatalf("bands = %d, want 2", chart.Bands)
	}
	baseline := chart.Lines[5]
	if baseline[0].Rune != '─' && baseline[0].Rune != ' ' {
		t.Fatalf("baseline starts with %q", baseline[0].Rune)
	}
	reversed := 0
	for _, row := range chart.Lines {
		for _, cell := range row {
			if cell.Reversed {
				reversed++
			}
		}
	}
	if reversed == 0 {
		t.Fatal("negative band painted no reversed cells")
	}
}

// TestAnalyticsPlotMissingZeroAndNarrowLikeRust mirrors Rust's
// missing_zero_and_narrow_charts_keep_the_selection_visible.
func TestAnalyticsPlotMissingZeroAndNarrowLikeRust(t *testing.T) {
	zero := plotTestDay(t, "2026-01-14", 0, plotTestValue("a", 0))
	used := plotTestDay(t, "2026-01-15", 5, plotTestValue("a", 5))
	days := []AnalyticsPlotDay{
		{Date: plotTestDate(t, "2026-01-13")},
		{Date: zero.Date, Day: zero},
		{Date: used.Date, Day: used},
	}
	for _, test := range []struct{ width, cursor int }{{36, 0}, {36, 1}, {8, 2}, {1, 2}} {
		chart := AnalyticsPlot(days, used.Values, test.cursor, test.width, 3, AccountAnalyticsUnitCount)
		if chart.Bands != 1 {
			t.Fatalf("width=%d bands = %d, want 1", test.width, chart.Bands)
		}
		for index := range chart.Lines {
			if got := plotTestLineWidth(chart, index); got > test.width {
				t.Fatalf("width=%d line %d width = %d", test.width, index, got)
			}
		}
		markers := 0
		for _, row := range chart.Lines {
			for _, cell := range row {
				if cell.Rune == '▲' {
					markers++
				}
			}
		}
		if markers != 1 {
			t.Fatalf("width=%d cursor=%d markers = %d, want 1", test.width, test.cursor, markers)
		}
		if test.cursor == 0 {
			dashLine, dashColumn := plotFindRune(chart, '—')
			cursorLine, cursorColumn := plotFindRune(chart, '▲')
			if dashLine < 0 || cursorLine < 0 || dashColumn != cursorColumn {
				t.Fatalf("dash at (%d,%d), cursor at (%d,%d)", dashLine, dashColumn, cursorLine, cursorColumn)
			}
		}
	}
}

func plotFindRune(chart AnalyticsChart, want rune) (int, int) {
	for line, row := range chart.Lines {
		for column, cell := range row {
			if cell.Rune == want {
				return line, column
			}
		}
	}
	return -1, -1
}

// TestAnalyticsPlotHidesOversizedAxesLikeRust mirrors Rust's
// oversized_axes_are_hidden_instead_of_truncated.
func TestAnalyticsPlotHidesOversizedAxesLikeRust(t *testing.T) {
	day := plotTestDay(t, "2026-01-15", 100_000_000, plotTestValue("a", 100_000_000))
	days := plotTestDays(day)
	for _, width := range []int{8, 30, 35, 44} {
		chart := AnalyticsPlot(days, day.Values, 0, width, 4, AccountAnalyticsUnitTokens)
		baseline := plotTestLineTexts(chart)[5]
		// Rust #45769 compacts the axis labels and still hides the gutter below
		// the 30-column minimum (plot_tests.rs oversized_axes_are_hidden...).
		if startsWithAxis := strings.HasPrefix(baseline, "─"); startsWithAxis != (width < 30) {
			t.Fatalf("width=%d baseline starts with axis = %v", width, startsWithAxis)
		}
		if width == 8 && !strings.Contains(plotTestLineTexts(chart)[0], "100M") {
			t.Fatalf("width=8 first row = %q, want the compact peak", plotTestLineTexts(chart)[0])
		}
	}
}

// TestAnalyticsPlotCalloutKeepsAuthoritativeTotalLikeRust mirrors Rust's
// selected_callout_keeps_the_authoritative_daily_total.
func TestAnalyticsPlotCalloutKeepsAuthoritativeTotalLikeRust(t *testing.T) {
	day := plotTestDay(t, "2026-01-15", 20, plotTestValue("a", 8), plotTestValue("b", 4))
	chart := AnalyticsPlot(plotTestDays(day), day.Values, 0, 44, 4, AccountAnalyticsUnitCredits)
	text := AnalyticsChartText(chart)
	if !strings.Contains(text, "20.00") {
		t.Fatalf("chart text = %q, want the authoritative total", text)
	}
	if strings.Contains(text, "12.00") {
		t.Fatalf("chart text = %q, want the plotted parts not to replace the total", text)
	}
}

// TestAnalyticsPlotNeverTruncatesNarrowDatesLikeRust mirrors Rust's
// narrow_long_range_charts_never_truncate_dates.
func TestAnalyticsPlotNeverTruncatesNarrowDatesLikeRust(t *testing.T) {
	start := plotTestDate(t, "2026-01-01")
	rows := make([]*AccountAnalyticsDay, 0, 30)
	for offset := 0; offset < 30; offset++ {
		date := start.AddDate(0, 0, offset)
		rows = append(rows, &AccountAnalyticsDay{Date: date, Total: 1, Values: []AccountAnalyticsValue{plotTestValue("a", 1)}})
	}
	days := plotTestDays(rows...)
	for _, test := range []struct {
		width    int
		expected string
	}{{1, ""}, {2, "15"}, {5, "15"}, {6, "Jan 15"}} {
		chart := AnalyticsPlot(days, rows[0].Values, 14, test.width, 3, AccountAnalyticsUnitCount)
		lines := plotTestLineTexts(chart)
		if got := strings.TrimSpace(lines[len(lines)-1]); got != test.expected {
			t.Fatalf("width=%d last row = %q, want %q", test.width, got, test.expected)
		}
	}
}

// TestAnalyticsPlotPartialCellsUseMidpointLikeRust mirrors Rust's
// partial_cells_use_the_midpoint_and_descend_from_the_baseline.
func TestAnalyticsPlotPartialCellsUseMidpointLikeRust(t *testing.T) {
	categories := []AccountAnalyticsValue{plotTestValue("a", 0.8), plotTestValue("b", 0.95)}
	for _, sign := range []float64{1, -1} {
		day := plotTestDay(t, "2026-01-14", 1.75*sign,
			plotTestValue("a", 0.8*sign), plotTestValue("b", 0.95*sign))
		days := []AnalyticsPlotDay{
			{Date: day.Date, Day: day},
			{Date: plotTestDate(t, "2026-01-15")},
		}
		chart := AnalyticsPlot(days, categories, 1, 12, 1, AccountAnalyticsUnitCredits)
		row := 1
		wantGlyph := '▇'
		if sign < 0 {
			row = 3
			wantGlyph = '▁'
		}
		cell := chart.Lines[row][1]
		if cell.Series != 1 {
			t.Fatalf("sign=%v series = %d, want 1 (second category)", sign, cell.Series)
		}
		if cell.Rune != wantGlyph {
			t.Fatalf("sign=%v glyph = %q, want %q", sign, cell.Rune, wantGlyph)
		}
		if cell.Reversed != (sign < 0) {
			t.Fatalf("sign=%v reversed = %v", sign, cell.Reversed)
		}
	}
}

// TestAnalyticsPlotTinySegmentsKeepSeriesColorLikeRust mirrors Rust's
// tiny_segments_keep_their_series_color_beside_a_large_day.
func TestAnalyticsPlotTinySegmentsKeepSeriesColorLikeRust(t *testing.T) {
	large := plotTestDay(t, "2026-01-14", 100, plotTestValue("a", 100))
	for _, sign := range []float64{1, -1} {
		tiny := plotTestDay(t, "2026-01-15", 0.01*sign, plotTestValue("a", 0.01*sign))
		chart := AnalyticsPlot(plotTestDays(large, tiny), large.Values, 0, 44, 4, AccountAnalyticsUnitCredits)
		want := '▁'
		if sign < 0 {
			want = '▇'
		}
		found := false
		for _, row := range chart.Lines {
			for _, cell := range row {
				if cell.Rune != want {
					continue
				}
				found = true
				if cell.Series != 0 {
					t.Fatalf("sign=%v tiny cell series = %d, want 0", sign, cell.Series)
				}
			}
		}
		if !found {
			t.Fatalf("sign=%v tiny segment was not painted", sign)
		}
	}
}

// TestAnalyticsPlotSevenDayTotalsLikeRust mirrors Rust's seven-day totals:
// a short range labels every non-empty bar without selecting each one.
func TestAnalyticsPlotSevenDayTotalsLikeRust(t *testing.T) {
	start := plotTestDate(t, "2026-01-01")
	rows := make([]*AccountAnalyticsDay, 0, 7)
	for offset := 0; offset < 7; offset++ {
		total := 111 + float64(offset)*100
		rows = append(rows, &AccountAnalyticsDay{
			Date:   start.AddDate(0, 0, offset),
			Total:  total,
			Values: []AccountAnalyticsValue{plotTestValue("a", total)},
		})
	}
	chart := AnalyticsPlot(plotTestDays(rows...), rows[0].Values, 3, 100, 8, AccountAnalyticsUnitCount)
	text := AnalyticsChartText(chart)
	for _, row := range rows {
		want := analyticsAmount(row.Total)
		if !strings.Contains(text, want) {
			t.Fatalf("chart text is missing daily total %q:\n%s", want, text)
		}
	}
}
