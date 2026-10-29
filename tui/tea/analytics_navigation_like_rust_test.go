package tea

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"codex_go/tui/analytics"
)

// readyAnalyticsHistoryLoad settles an in-memory history into a ready load so a
// test can exercise rendering without a report round trip.
func readyAnalyticsHistoryLoad(t *testing.T, history analytics.AccountAnalyticsHistory) analytics.AnalyticsLoad[analytics.AccountAnalyticsHistory] {
	t.Helper()
	load := analytics.StartAnalyticsLoad(context.Background(), func(context.Context) (*analytics.AccountAnalyticsHistory, error) {
		return &history, nil
	}, nil)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		load.Poll()
		if _, ok := load.Ready(); ok {
			return *load
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("history load did not settle")
	return *load
}

func analyticsLegendHistory(t *testing.T, dashboard *analyticsDashboard) analytics.AccountAnalyticsHistory {
	t.Helper()
	values := make([]analytics.AccountAnalyticsValue, 0, 10)
	for index := 0; index < 10; index++ {
		values = append(values, analytics.AccountAnalyticsValue{
			Key:   fmt.Sprintf("feature-%d", index),
			Label: fmt.Sprintf("Feature %d", index+1),
			Value: 10,
		})
	}
	data := make([]analytics.AccountAnalyticsDay, 0, 7)
	for offset := 6; offset >= 0; offset-- {
		data = append(data, analytics.AccountAnalyticsDay{
			Date:   dashboard.endDate.AddDate(0, 0, -offset),
			Total:  100,
			Values: values,
		})
	}
	updated := int64(1_788_364_800)
	return analytics.AccountAnalyticsHistory{
		Unit:      analytics.AccountAnalyticsUnitCredits,
		UpdatedAt: &updated,
		Data:      data,
	}
}

// TestAnalyticsNavigationKeepsItsRowLikeRust covers Rust #45770: the range row
// keeps a stable height so the section tabs never shift between sections.
func TestAnalyticsNavigationKeepsItsRowLikeRust(t *testing.T) {
	backend := startAnalyticsTestBackend(t, "account-a", "plus", `{"data": []}`)
	for _, width := range []int{64, 120} {
		model := newAnalyticsTestModel(t, "plus", backend)
		model.width, model.height = width, 40
		openAnalyticsFromUsageMenu(t, model)
		waitForAnalyticsLoads(t, model)

		dashboard := model.analyticsDashboard
		positions := make([]int, 0, 4)
		for _, section := range []analytics.AnalyticsSection{
			analytics.AnalyticsSectionSummary,
			analytics.AnalyticsSectionUsage,
			analytics.AnalyticsSectionChats,
			analytics.AnalyticsSectionSummary,
		} {
			dashboard.selectSection(section)
			position := -1
			for index, line := range strings.Split(stripAnalyticsTestANSI(model.View()), "\n") {
				if strings.Contains(line, "Summary") && strings.Contains(line, "usage history") {
					position = index
					break
				}
			}
			if position < 0 {
				t.Fatalf("width=%d section %v missing the tab row", width, section)
			}
			positions = append(positions, position)
		}
		for _, position := range positions {
			if position != positions[0] {
				t.Fatalf("width=%d tab rows = %v, want all %d", width, positions, positions[0])
			}
		}
	}
}

// TestAnalyticsTallerReportShowsMoreLegendRowsLikeRust covers Rust #45770's
// adaptive legend and the footer update timestamp.
func TestAnalyticsTallerReportShowsMoreLegendRowsLikeRust(t *testing.T) {
	backend := startAnalyticsTestBackend(t, "account-a", "plus", `{"data": []}`)
	model := newAnalyticsTestModel(t, "plus", backend)
	openAnalyticsFromUsageMenu(t, model)
	waitForAnalyticsLoads(t, model)

	dashboard := model.analyticsDashboard
	dashboard.selectSection(analytics.AnalyticsSectionUsage)
	dashboard.sections[analytics.AnalyticsSectionUsage].History = readyAnalyticsHistoryLoad(t, analyticsLegendHistory(t, dashboard))

	model.width, model.height = 120, 30
	short := stripAnalyticsTestANSI(model.View())
	model.width, model.height = 120, 60
	tall := stripAnalyticsTestANSI(model.View())
	if strings.Count(tall, "\u25cf ") <= strings.Count(short, "\u25cf ") {
		t.Fatalf("tall legend rows = %d, short = %d", strings.Count(tall, "\u25cf "), strings.Count(short, "\u25cf "))
	}
	last := strings.Split(strings.TrimRight(tall, "\n"), "\n")
	start := len(last) - 3
	if start < 0 {
		start = 0
	}
	found := false
	for _, line := range last[start:] {
		if strings.Contains(line, "Updated") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("tall footer missing the update timestamp:\n%s", tall)
	}
	if got := strings.Count(short, "Updated"); got != 1 {
		t.Fatalf("update timestamp appears %d times, want once in the footer:\n%s", got, short)
	}
}

// TestAnalyticsScrollHintsFollowOverflowLikeRust covers Rust #45770: scroll
// hints disappear when the content fits and return when it overflows.
func TestAnalyticsScrollHintsFollowOverflowLikeRust(t *testing.T) {
	backend := startAnalyticsTestBackend(t, "account-a", "plus", `{"data": []}`)
	model := newAnalyticsTestModel(t, "plus", backend)
	openAnalyticsFromUsageMenu(t, model)
	waitForAnalyticsLoads(t, model)

	dashboard := model.analyticsDashboard
	dashboard.selectSection(analytics.AnalyticsSectionUsage)
	dashboard.sections[analytics.AnalyticsSectionUsage].History = readyAnalyticsHistoryLoad(t, analyticsLegendHistory(t, dashboard))

	model.width, model.height = 120, 80
	roomy := stripAnalyticsTestANSI(model.View())
	if strings.Contains(roomy, "scroll") {
		t.Fatalf("fitted report kept the scroll hint:\n%s", roomy)
	}
	model.width, model.height = 120, 20
	cramped := stripAnalyticsTestANSI(model.View())
	if !strings.Contains(cramped, "scroll") {
		t.Fatalf("overflowing report dropped the scroll hint:\n%s", cramped)
	}
}
