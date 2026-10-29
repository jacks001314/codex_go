package analytics

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func chartLayoutTestHistory(t *testing.T) *AccountAnalyticsHistory {
	t.Helper()
	end := time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC)
	values := make([]AccountAnalyticsValue, 0, 10)
	for index := 0; index < 10; index++ {
		values = append(values, AccountAnalyticsValue{
			Key:   fmt.Sprintf("feature-%d", index),
			Label: fmt.Sprintf("Feature %d", index+1),
			Value: 10,
		})
	}
	return &AccountAnalyticsHistory{
		Unit: AccountAnalyticsUnitCredits,
		Data: []AccountAnalyticsDay{{Date: end, Total: 100, Values: values}},
	}
}

// TestAnalyticsHistoryBreakdownRowBudgetLikeRust covers Rust #45770's
// viewport-dependent breakdown rows: a taller maximized report keeps more rows
// before folding the rest into the remainder.
func TestAnalyticsHistoryBreakdownRowBudgetLikeRust(t *testing.T) {
	history := chartLayoutTestHistory(t)
	state := DefaultAnalyticsSectionState()
	opts := AnalyticsHistoryOptions{
		Width:          60,
		ChartHeight:    4,
		RangeIndex:     0,
		EndDate:        history.Data[0].Date,
		Zoomed:         true,
		ViewportHeight: 8,
	}
	short, _ := AnalyticsHistoryLines(AnalyticsSectionUsage, state, history, opts)
	shortText := strings.Join(short, "\n")
	if !strings.Contains(shortText, "7 more") {
		t.Fatalf("short report missing the remainder row:\n%s", shortText)
	}
	if got := strings.Count(shortText, "\u25cf "); got != 3 {
		t.Fatalf("short report rows = %d, want 3", got)
	}

	opts.ViewportHeight = 40
	tall, _ := AnalyticsHistoryLines(AnalyticsSectionUsage, state, history, opts)
	tallText := strings.Join(tall, "\n")
	if strings.Contains(tallText, "more") {
		t.Fatalf("tall report kept the remainder row:\n%s", tallText)
	}
	if got := strings.Count(tallText, "\u25cf "); got != 10 {
		t.Fatalf("tall report rows = %d, want 10", got)
	}
}

// TestAnalyticsHistoryTimestampMovesToFooterLikeRust covers Rust #45770: a
// maximized report leaves the update timestamp to the viewport footer while the
// overview card still prints it under the table.
func TestAnalyticsHistoryTimestampMovesToFooterLikeRust(t *testing.T) {
	history := chartLayoutTestHistory(t)
	updated := int64(1_788_364_800)
	history.UpdatedAt = &updated
	state := DefaultAnalyticsSectionState()
	opts := AnalyticsHistoryOptions{
		Width:          60,
		ChartHeight:    4,
		RangeIndex:     0,
		EndDate:        history.Data[0].Date,
		Zoomed:         true,
		ViewportHeight: 40,
	}
	zoomed, _ := AnalyticsHistoryLines(AnalyticsSectionUsage, state, history, opts)
	if text := strings.Join(zoomed, "\n"); strings.Contains(text, "Updated ") {
		t.Fatalf("maximized report kept the body timestamp:\n%s", text)
	}

	opts.Zoomed = false
	overview, _ := AnalyticsHistoryLines(AnalyticsSectionUsage, state, history, opts)
	if text := strings.Join(overview, "\n"); !strings.Contains(text, "Updated ") || !strings.Contains(text, "UTC") {
		t.Fatalf("overview card missing the timestamp:\n%s", text)
	}
}
