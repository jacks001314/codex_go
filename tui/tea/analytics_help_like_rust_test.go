package tea

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	codextui "codex_go/tui"
	"codex_go/tui/analytics"
	"codex_go/tui/keymsg"
)

// analyticsErrorHistoryLoad settles an error load so the fixed chrome can be
// checked against a failed report.
func analyticsErrorHistoryLoad(t *testing.T) analytics.AnalyticsLoad[analytics.AccountAnalyticsHistory] {
	t.Helper()
	load := analytics.StartAnalyticsLoad(context.Background(), func(context.Context) (*analytics.AccountAnalyticsHistory, error) {
		return nil, errors.New("report failed")
	}, nil)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		load.Poll()
		if load.State() == analytics.AnalyticsLoadStateError {
			return *load
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("error load did not settle")
	return analytics.UnavailableAnalyticsLoad[analytics.AccountAnalyticsHistory]()
}

// TestAnalyticsHelpPreservesReportStateAndScrollLikeRust covers #46863: help
// has its own scroll position and dismissing it restores the exact report
// selection, expanded detail, and reading position.
func TestAnalyticsHelpPreservesReportStateAndScrollLikeRust(t *testing.T) {
	backend := startAnalyticsTestBackend(t, "account-a", "plus", "{\"data\": []}")
	model := newAnalyticsTestModel(t, "plus", backend)
	model.width, model.height = 100, 10
	openAnalyticsFromUsageMenu(t, model)
	waitForAnalyticsLoads(t, model)

	dashboard := model.analyticsDashboard
	dashboard.selectSection(analytics.AnalyticsSectionUsage)
	detail := 3
	dashboard.sections[analytics.AnalyticsSectionUsage].Cursor = 3
	dashboard.sections[analytics.AnalyticsSectionUsage].Detail = &detail
	_ = model.viewString()
	dashboard.scrollOffset = 5

	model.Update(runes("?"))
	if !dashboard.showHelp {
		t.Fatal("? did not open the usage help")
	}
	if dashboard.helpScroll != 0 {
		t.Fatalf("opening help scroll = %d, want 0", dashboard.helpScroll)
	}
	if got := dashboard.scrollOffset; got != 5 {
		t.Fatalf("opening help changed report scroll to %d", got)
	}
	model.Update(key(keymsg.KeyPgDown))
	if dashboard.helpScroll == 0 {
		t.Fatal("page down did not scroll the help body")
	}
	if got := dashboard.scrollOffset; got != 5 {
		t.Fatalf("help page down changed report scroll to %d", got)
	}
	model.Update(runes("?"))
	if dashboard.showHelp {
		t.Fatal("? did not close the usage help")
	}
	if got := dashboard.scrollOffset; got != 5 {
		t.Fatalf("closing help changed report scroll to %d", got)
	}
	if dashboard.section != analytics.AnalyticsSectionUsage {
		t.Fatalf("closing help changed section to %v", dashboard.section)
	}
	state := dashboard.sections[analytics.AnalyticsSectionUsage]
	if state.Cursor != 3 || state.Detail == nil || *state.Detail != 3 {
		t.Fatalf("closing help changed the report selection: %+v", state)
	}

	model.Update(runes("?"))
	model.Update(key(keymsg.KeyEsc))
	if dashboard.showHelp {
		t.Fatal("esc did not close the usage help")
	}
	if dashboard.section != analytics.AnalyticsSectionUsage ||
		dashboard.sections[analytics.AnalyticsSectionUsage].Detail == nil {
		t.Fatal("esc from help changed the report state")
	}
}

// TestAnalyticsHelpShortcutYieldsToConfiguredBindingLikeRust keeps a custom
// list binding authoritative when it claims the ? key.
func TestAnalyticsHelpShortcutYieldsToConfiguredBindingLikeRust(t *testing.T) {
	backend := startAnalyticsTestBackend(t, "account-a", "enterprise", "{\"data\": []}")
	model := newAnalyticsTestModel(t, "enterprise", backend)
	config := codextui.NewKeymapConfig()
	if err := config.Set("list", "jump_top", []string{"?"}); err != nil {
		t.Fatalf("configure jump_top: %v", err)
	}
	model.keymapConfig = config
	openAnalyticsFromUsageMenu(t, model)
	waitForAnalyticsLoads(t, model)

	dashboard := model.analyticsDashboard
	dashboard.selectSection(analytics.AnalyticsSectionUsage)
	dashboard.sections[analytics.AnalyticsSectionUsage].Cursor = 4
	model.Update(runes("?"))
	if dashboard.showHelp {
		t.Fatal("? opened help despite the configured jump_top binding")
	}
	if dashboard.sections[analytics.AnalyticsSectionUsage].Cursor != 0 {
		t.Fatalf("? cursor = %d, want the configured jump top", dashboard.sections[analytics.AnalyticsSectionUsage].Cursor)
	}
}

// TestAnalyticsFixedControlsAndFooterLikeRust keeps the tab row and the two
// footer hint rows at fixed positions while reports load and fail.
func TestAnalyticsFixedControlsAndFooterLikeRust(t *testing.T) {
	backend := startAnalyticsTestBackend(t, "account-a", "plus", "{\"data\": []}")
	for _, width := range []int{64, 120} {
		model := newAnalyticsTestModel(t, "plus", backend)
		model.width, model.height = width, 32
		openAnalyticsFromUsageMenu(t, model)
		waitForAnalyticsLoads(t, model)
		dashboard := model.analyticsDashboard

		tabRow := func() int {
			lines := strings.Split(stripAnalyticsTestANSI(model.viewString()), "\n")
			for index, line := range lines {
				if strings.Contains(line, "Overview") && strings.Contains(line, "Usage") {
					return index
				}
			}
			return -1
		}
		before := tabRow()
		if before < 0 {
			t.Fatalf("width=%d missing the compact report tabs:\n%s", width, model.viewString())
		}
		dashboard.selectSection(analytics.AnalyticsSectionUsage)
		after := tabRow()
		if after != before {
			t.Fatalf("width=%d tab row moved from %d to %d", width, before, after)
		}
		dashboard.sections[analytics.AnalyticsSectionUsage].History =
			analyticsErrorHistoryLoad(t)
		after = tabRow()
		if after != before {
			t.Fatalf("width=%d tab row moved after a report failure from %d to %d", width, before, after)
		}
		lines := strings.Split(strings.TrimRight(stripAnalyticsTestANSI(model.viewString()), "\n"), "\n")
		if len(lines) != 32 {
			t.Fatalf("width=%d rendered %d rows, want the full viewport", width, len(lines))
		}
		if !strings.Contains(lines[len(lines)-2], "back") || !strings.Contains(lines[len(lines)-1], "refresh") {
			t.Fatalf("width=%d footer rows = %q / %q", width, lines[len(lines)-2], lines[len(lines)-1])
		}
		for _, line := range lines[len(lines)-2:] {
			if strings.Contains(line, "Updated") {
				t.Fatalf("width=%d update metadata leaked into the footer: %q", width, line)
			}
		}
	}
}

// TestAnalyticsControlsShowRangeGroupModelAndUpdateLikeRust covers the fixed
// controls above the report, including the cross-year numeric range fallback.
func TestAnalyticsControlsShowRangeGroupModelAndUpdateLikeRust(t *testing.T) {
	backend := startAnalyticsTestBackend(t, "account-a", "business", "{\"data\": []}")
	model := newAnalyticsTestModel(t, "business", backend)
	model.width, model.height = 40, 30
	openAnalyticsFromUsageMenu(t, model)
	waitForAnalyticsLoads(t, model)

	dashboard := model.analyticsDashboard
	dashboard.selectSection(analytics.AnalyticsSectionUsage)
	dashboard.sections[analytics.AnalyticsSectionUsage].History = readyAnalyticsHistoryLoad(t, analyticsLegendHistory(t, dashboard))
	dashboard.endDate = time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC)
	dashboard.ranges[analytics.AnalyticsRangeGroupActivity] = 1
	plain := stripAnalyticsTestANSI(model.viewString())
	for _, want := range []string{"r 7d/30d", "12/17/2025–01/15/2026", "m All models", "Updated · Sep 2 16:00 UTC"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("controls missing %q:\n%s", want, plain)
		}
	}
	lines := strings.Split(plain, "\n")
	if !strings.Contains(lines[3], "r 7d/30d") || !strings.Contains(lines[4], "m All models") {
		t.Fatalf("narrow control rows = %q / %q", lines[3], lines[4])
	}
}

// TestAnalyticsHelpControlAvailabilityFollowsDetailLikeRust shares the
// zero-credit action gate between key dispatch and help.
func TestAnalyticsHelpControlAvailabilityFollowsDetailLikeRust(t *testing.T) {
	backend := startAnalyticsChatsTestBackend(t, "business", map[string]int64{"chat-a": 2_000_000})
	model := newAnalyticsTestModel(t, "business", backend)
	model.analyticsChats = func(ctx context.Context, cursor *string, limit int) (analytics.AnalyticsChatPage, error) {
		return analytics.AnalyticsChatPage{Data: []analytics.AnalyticsChatThread{
			{ID: "chat-a", Name: analyticsChatTestPtr("With estimate"), UpdatedAt: time.Now().Unix()},
		}}, nil
	}
	openAnalyticsFromUsageMenu(t, model)
	waitForAnalyticsLoads(t, model)
	dashboard := model.analyticsDashboard
	dashboard.selectSection(analytics.AnalyticsSectionChats)
	waitForAnalyticsLoads(t, model)
	model.Update(key(keymsg.KeyEnter))
	if detail := dashboard.sections[analytics.AnalyticsSectionChats].Detail; detail == nil || *detail != 0 {
		t.Fatalf("chat detail = %v, want row 0", detail)
	}

	model.Update(runes("?"))
	plain := stripAnalyticsTestANSI(model.viewString())
	if !strings.Contains(plain, "a · show zero-credit groups in expanded details") {
		t.Fatalf("help missing the available zero-credit action:\n%s", plain)
	}
	if strings.Contains(plain, "s · change chat sort metric") {
		t.Fatalf("business help offered the consumer sort action:\n%s", plain)
	}
	model.Update(runes("?"))
	dashboard.sections[analytics.AnalyticsSectionChats].Detail = nil
	model.Update(runes("?"))
	plain = stripAnalyticsTestANSI(model.viewString())
	if strings.Contains(plain, "a · show zero-credit groups in expanded details") {
		t.Fatalf("help offered zero-credit actions without an expanded detail:\n%s", plain)
	}
}

// TestAnalyticsNarrowHelpWrapsLikeRust checks the scrollable help body on a
// narrow terminal and the fixed shortcut/navigation hint rows.
func TestAnalyticsNarrowHelpWrapsLikeRust(t *testing.T) {
	backend := startAnalyticsTestBackend(t, "account-a", "plus", "{\"data\": []}")
	model := newAnalyticsTestModel(t, "plus", backend)
	model.width, model.height = 40, 30
	openAnalyticsFromUsageMenu(t, model)
	waitForAnalyticsLoads(t, model)
	model.Update(runes("?"))
	plain := stripAnalyticsTestANSI(model.viewString())
	for _, want := range []string{"Usage shortcuts", "select chat or plan", "toggle this help", "close help"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("narrow help missing %q:\n%s", want, plain)
		}
	}
	for index, line := range strings.Split(plain, "\n") {
		if analytics.AnalyticsDisplayWidth(line) > 40 {
			t.Fatalf("help line %d exceeds width 40: %q", index, line)
		}
	}
}
