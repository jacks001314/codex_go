package tea

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	codextui "codex_go/tui"
	"codex_go/tui/analytics"
	"codex_go/tui/keymsg"
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
			for index, line := range strings.Split(stripAnalyticsTestANSI(model.viewString()), "\n") {
				if strings.Contains(line, "Overview") && strings.Contains(line, "Usage") {
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
	short := stripAnalyticsTestANSI(model.viewString())
	model.width, model.height = 120, 60
	tall := stripAnalyticsTestANSI(model.viewString())
	if strings.Count(tall, "\u25cf ") <= strings.Count(short, "\u25cf ") {
		t.Fatalf("tall legend rows = %d, short = %d", strings.Count(tall, "\u25cf "), strings.Count(short, "\u25cf "))
	}
	// #46863 moved the report timestamp into the fixed controls area above
	// the body. It stays in the first six rows at every width.
	lines := strings.Split(strings.TrimRight(tall, "\n"), "\n")
	found := false
	for _, line := range lines[:analyticsMin(6, len(lines))] {
		if strings.Contains(line, "Updated") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("tall controls missing the update timestamp:\n%s", tall)
	}
	if got := strings.Count(short, "Updated"); got != 1 {
		t.Fatalf("update timestamp appears %d times, want once in the controls:\n%s", got, short)
	}
	// Narrow layouts reserve a third control row and keep the range control on
	// row three (Rust #46863 account_layout_tests).
	for _, width := range []int{24, 40, 64} {
		model.width, model.height = width, 60
		lines := strings.Split(strings.TrimRight(stripAnalyticsTestANSI(model.viewString()), "\n"), "\n")
		row := ""
		if len(lines) > 3 {
			row = lines[3]
		}
		if !strings.Contains(row, "7d/30d") {
			t.Fatalf("width=%d control row = %q, want the 7d/30d range", width, row)
		}
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
	roomy := stripAnalyticsTestANSI(model.viewString())
	if strings.Contains(roomy, "scroll") {
		t.Fatalf("fitted report kept the scroll hint:\n%s", roomy)
	}
	model.width, model.height = 120, 20
	cramped := stripAnalyticsTestANSI(model.viewString())
	if !strings.Contains(cramped, "scroll") {
		t.Fatalf("overflowing report dropped the scroll hint:\n%s", cramped)
	}
}

// TestAnalyticsExactFitHasNoScrollHintLikeRust covers Rust #45770: content that
// exactly fills the viewport drops the scroll hint and leaves the viewport at
// the content height (Rust analytics_exact_fit_content_has_no_scroll_hint).
func TestAnalyticsExactFitHasNoScrollHintLikeRust(t *testing.T) {
	backend := startAnalyticsTestBackend(t, "account-a", "enterprise", `{"data": []}`)
	model := newAnalyticsTestModel(t, "enterprise", backend)
	openAnalyticsFromUsageMenu(t, model)
	waitForAnalyticsLoads(t, model)

	dashboard := model.analyticsDashboard
	dashboard.selectSection(analytics.AnalyticsSectionChats)

	model.width, model.height = 120, 60
	if strings.Contains(stripAnalyticsTestANSI(model.viewString()), "scroll") {
		t.Fatal("roomy chats panel kept the scroll hint")
	}
	chromeHeight := 60 - dashboard.viewportHeight
	contentHeight := len(dashboard.panel(analytics.AnalyticsSectionChats, 116, 4).Lines)

	model.height = chromeHeight + contentHeight
	exact := stripAnalyticsTestANSI(model.viewString())
	if strings.Contains(exact, "scroll") {
		t.Fatalf("exact fit kept the scroll hint:\n%s", exact)
	}
	if dashboard.viewportHeight != contentHeight {
		t.Fatalf("viewport height = %d, want the %d content rows", dashboard.viewportHeight, contentHeight)
	}
}

// TestAnalyticsUngroupedSectionsKeepConfiguredGLikeRust covers Rust #45770:
// when a section has no alternate grouping, a configured g jump-top binding
// still reaches the list action instead of being swallowed by grouping.
func TestAnalyticsUngroupedSectionsKeepConfiguredGLikeRust(t *testing.T) {
	backend := startAnalyticsTestBackend(t, "account-a", "enterprise", `{"data": []}`)
	model := newAnalyticsTestModel(t, "enterprise", backend)
	keymapConfig := codextui.NewKeymapConfig()
	if err := keymapConfig.Set("list", "jump_top", []string{"g"}); err != nil {
		t.Fatalf("configure jump_top: %v", err)
	}
	model.keymapConfig = keymapConfig
	openAnalyticsFromUsageMenu(t, model)
	waitForAnalyticsLoads(t, model)

	dashboard := model.analyticsDashboard
	// The Rust fixture has no live session, so credits expose one grouping.
	dashboard.live = nil
	for _, section := range []analytics.AnalyticsSection{
		analytics.AnalyticsSectionChats,
		analytics.AnalyticsSectionPlugins,
		analytics.AnalyticsSectionSkills,
		analytics.AnalyticsSectionCredits,
	} {
		dashboard.selectSection(section)
		if options := dashboard.groupOptions(); len(options) > 1 {
			t.Fatalf("%v unexpectedly groups by %v", section, options)
		}
		dashboard.sections[section].Cursor = 3
		model.Update(keymsg.KeyMsg{Type: keymsg.KeyRunes, Runes: []rune{'g'}})
		if cursor := dashboard.sections[section].Cursor; cursor != 0 {
			t.Fatalf("%v cursor after g = %d, want the configured jump top", section, cursor)
		}
	}
}
