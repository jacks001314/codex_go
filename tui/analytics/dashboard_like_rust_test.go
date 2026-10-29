package analytics

import (
	"strings"
	"testing"
	"time"

	"codex_go/auth"
)

// TestAnalyticsToolNameMatchesRust covers Rust's tool_panel::tool_name (#45764).
func TestAnalyticsToolNameMatchesRust(t *testing.T) {
	cases := map[string]string{
		"openai:openai":            "OpenAI",
		"acme:acme":                "acme",
		"github pr review":         "github PR review",
		"codex cli":                "codex CLI",
		"namespace:other":          "namespace:other",
		" mcp server  api ":        "MCP server API",
		"tui sdk ide ui":           "TUI SDK IDE UI",
		"unknown words stay as-is": "unknown words stay as-is",
	}
	for input, expected := range cases {
		if got := AnalyticsToolName(input); got != expected {
			t.Errorf("AnalyticsToolName(%q) = %q, want %q", input, got, expected)
		}
	}
}

// TestAnalyticsVisibleSectionsMatchRust covers Rust's account-specific card
// order.
func TestAnalyticsVisibleSectionsMatchRust(t *testing.T) {
	consumer := AnalyticsVisibleSections(auth.PlanPlus, true)
	want := []AnalyticsSection{AnalyticsSectionSummary, AnalyticsSectionUsage, AnalyticsSectionActivity, AnalyticsSectionPlugins, AnalyticsSectionSkills, AnalyticsSectionChats}
	if !analyticsSameSections(consumer, want) {
		t.Fatalf("consumer sections = %v, want %v", consumer, want)
	}
	business := AnalyticsVisibleSections(auth.PlanBusiness, true)
	want = []AnalyticsSection{AnalyticsSectionSummary, AnalyticsSectionCredits, AnalyticsSectionUsage, AnalyticsSectionPlugins, AnalyticsSectionSkills, AnalyticsSectionChats}
	if !analyticsSameSections(business, want) {
		t.Fatalf("business sections = %v, want %v", business, want)
	}
	// EnterpriseCbpUsageBased is business-like and exposes thread estimates.
	if enterprise := AnalyticsVisibleSections(auth.PlanEnterpriseCBPUsageBased, true); !analyticsSameSections(enterprise, want) {
		t.Fatalf("enterprise sections = %v, want %v", enterprise, want)
	}
	// Plans without thread-usage support keep the four-card layout even when
	// they are workspace accounts.
	want = []AnalyticsSection{AnalyticsSectionSummary, AnalyticsSectionCredits, AnalyticsSectionUsage, AnalyticsSectionPlugins, AnalyticsSectionSkills}
	if enterprise := AnalyticsVisibleSections(auth.PlanEnterprise, true); !analyticsSameSections(enterprise, want) {
		t.Fatalf("enterprise sections = %v, want %v", enterprise, want)
	}
	if ent26 := AnalyticsVisibleSections(auth.PlanEnt26, true); !analyticsSameSections(ent26, want) {
		t.Fatalf("ent26 sections = %v, want %v", ent26, want)
	}
	if team := AnalyticsVisibleSections(auth.PlanTeam, true); !analyticsSameSections(team, want) {
		t.Fatalf("team sections = %v, want %v", team, want)
	}
	if got := AnalyticsVisibleSections(auth.PlanUnknown, true); !analyticsSameSections(got, []AnalyticsSection{AnalyticsSectionSummary}) {
		t.Fatalf("unknown sections = %v, want only Summary", got)
	}
	if got := AnalyticsVisibleSections(auth.PlanPlus, false); len(got) != 0 {
		t.Fatalf("unresolved sections = %v, want none", got)
	}
}

func analyticsSameSections(left, right []AnalyticsSection) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

// TestAnalyticsRangeGroupsAndLabelsMatchRust covers range sharing and the
// "Product" grouping label rules.
func TestAnalyticsRangeGroupsAndLabelsMatchRust(t *testing.T) {
	if got := AnalyticsSectionUsage.RangeGroup(true); got != AnalyticsRangeGroupActivity {
		t.Fatalf("business usage range group = %v, want activity", got)
	}
	if got := AnalyticsSectionUsage.RangeGroup(false); got != AnalyticsRangeGroupUsage {
		t.Fatalf("consumer usage range group = %v, want usage", got)
	}
	if got := AnalyticsSectionPlugins.RangeGroup(true); got != AnalyticsRangeGroupTools {
		t.Fatalf("plugins range group = %v, want tools", got)
	}
	if got := AnalyticsSectionCredits.RangeGroup(false); got != AnalyticsRangeGroupUsage {
		t.Fatalf("credits range group = %v, want usage", got)
	}
	if got := AnalyticsSectionCredits.GroupLabel(0, true); got != "Product" {
		t.Fatalf("credits group 0 = %q, want Product", got)
	}
	if got := AnalyticsSectionUsage.GroupLabel(0, false); got != "Product" {
		t.Fatalf("unattributed usage group 0 = %q, want Product", got)
	}
	if got := AnalyticsSectionUsage.GroupLabel(0, true); got != "Surface" {
		t.Fatalf("attributed usage group 0 = %q, want Surface", got)
	}
	if got := AnalyticsSectionUsage.GroupLabel(1, false); got != "Feature" {
		t.Fatalf("usage group 1 = %q, want Feature", got)
	}
}

// TestAnalyticsGroupOptionsMatchRust covers Rust's AnalyticsView::group_options.
func TestAnalyticsGroupOptionsMatchRust(t *testing.T) {
	cases := []struct {
		section    AnalyticsSection
		business   bool
		attributed bool
		credits    []int
		want       []int
	}{
		{AnalyticsSectionUsage, true, false, nil, []int{6, 2}},
		{AnalyticsSectionUsage, false, true, nil, []int{1, 2, 0, 3}},
		{AnalyticsSectionUsage, false, false, nil, []int{0, 2}},
		{AnalyticsSectionCredits, true, false, []int{0, 1, 4}, []int{0, 1, 4}},
		{AnalyticsSectionCredits, true, false, nil, []int{0}},
		{AnalyticsSectionActivity, false, true, nil, []int{2, 0}},
		{AnalyticsSectionPlugins, true, false, nil, nil},
	}
	for _, test := range cases {
		got := AnalyticsGroupOptions(test.section, test.business, test.attributed, test.credits)
		if len(got) != len(test.want) {
			t.Fatalf("options(%v) = %v, want %v", test.section, got, test.want)
		}
		for index := range got {
			if got[index] != test.want[index] {
				t.Fatalf("options(%v) = %v, want %v", test.section, got, test.want)
			}
		}
	}
}

// TestAnalyticsConsumerAttributionAndTitlesMatchRust covers the attribution
// gate and the section titles it selects.
func TestAnalyticsConsumerAttributionAndTitlesMatchRust(t *testing.T) {
	attributed := true
	unattributed := false
	if !AnalyticsConsumerAttribution(false, nil) || !AnalyticsConsumerAttribution(false, &attributed) {
		t.Fatal("consumer attribution should default to and accept true")
	}
	if AnalyticsConsumerAttribution(false, &unattributed) {
		t.Fatal("consumer attribution should reject an incomplete report")
	}
	if AnalyticsConsumerAttribution(true, &attributed) {
		t.Fatal("business accounts do not use consumer attribution")
	}
	if got := AnalyticsSectionTitle(AnalyticsSectionUsage, true, false); got != "Token usage history" {
		t.Fatalf("business usage title = %q", got)
	}
	if got := AnalyticsSectionTitle(AnalyticsSectionUsage, false, true); got != "Total usage history" {
		t.Fatalf("attributed usage title = %q", got)
	}
	if got := AnalyticsSectionTitle(AnalyticsSectionUsage, false, false); got != "Usage history" {
		t.Fatalf("legacy usage title = %q", got)
	}
	if got := AnalyticsSectionTitle(AnalyticsSectionSkills, false, false); got != "Skills used" {
		t.Fatalf("skills title = %q", got)
	}
}

// TestAnalyticsRangeSwitchMovesSelectionLikeRust covers Rust's change_range
// cursor shift.
func TestAnalyticsRangeSwitchMovesSelectionLikeRust(t *testing.T) {
	detail := 3
	cursor, shiftedDetail := AnalyticsShiftSectionSelection(6, &detail, true)
	if cursor != 29 || shiftedDetail == nil || *shiftedDetail != 26 {
		t.Fatalf("to 30d = (%d, %v), want (29, 26)", cursor, shiftedDetail)
	}
	cursor, shiftedDetail = AnalyticsShiftSectionSelection(29, shiftedDetail, false)
	if cursor != 6 || shiftedDetail == nil || *shiftedDetail != 3 {
		t.Fatalf("back to 7d = (%d, %v), want (6, 3)", cursor, shiftedDetail)
	}
	cursor, shiftedDetail = AnalyticsShiftSectionSelection(2, nil, false)
	if cursor != 0 || shiftedDetail != nil {
		t.Fatalf("short range clamp = (%d, %v), want (0, nil)", cursor, shiftedDetail)
	}
	small := 5
	_, shiftedDetail = AnalyticsShiftSectionSelection(29, &small, false)
	if shiftedDetail != nil {
		t.Fatalf("detail %v should clear when it leaves the range", *shiftedDetail)
	}
}

// TestAnalyticsSectionDatesMatchRust covers the 7/30 day windows.
func TestAnalyticsSectionDatesMatchRust(t *testing.T) {
	end := time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC)
	if got := AnalyticsSectionStartDate(end, 0); !got.Equal(time.Date(2026, 1, 9, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("7d start = %v", got)
	}
	if got := AnalyticsSectionStartDate(end, 1); !got.Equal(time.Date(2025, 12, 17, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("30d start = %v", got)
	}
	if got := AnalyticsSectionDays(0); got != 7 {
		t.Fatalf("days(0) = %d", got)
	}
	if got := AnalyticsSectionDays(1); got != 30 {
		t.Fatalf("days(1) = %d", got)
	}
}

// TestAnalyticsDashboardLayoutMatchesRust covers the two-column overview, the
// viewport-driven card height, and the focused-card selection range.
func TestAnalyticsDashboardLayoutMatchesRust(t *testing.T) {
	visible := AnalyticsVisibleSections(auth.PlanPlus, true)
	opts := AnalyticsDashboardOptions{
		Width:    120,
		Height:   40,
		Visible:  visible,
		Section:  AnalyticsSectionUsage,
		Business: false,
		History:  func(section AnalyticsSection, width, height int) []string { return []string{"7d", "chart"} },
	}
	lines, start, end := AnalyticsDashboardLines(opts)
	if len(lines) != 50 {
		t.Fatalf("two-column rows = %d, want 50", len(lines))
	}
	if start != 0 || end != 16 {
		t.Fatalf("focused range = (%d,%d), want (0,16)", start, end)
	}
	if !strings.HasPrefix(lines[0], "╭─") || !strings.Contains(lines[0], "1 Summary") {
		t.Fatalf("first card top = %q", lines[0])
	}
	if !strings.Contains(lines[0], "2 Usage history") || !strings.Contains(lines[0], "▸") {
		t.Fatalf("focused card title = %q, want a focus marker", lines[0])
	}
	for index, line := range lines {
		if strings.HasPrefix(line, "╭") || strings.HasPrefix(line, "│") || strings.HasPrefix(line, "╰") {
			if got := analyticsDisplayWidth(line); got != 120 {
				t.Fatalf("line %d width = %d, want 120 (%q)", index, got, line)
			}
		}
	}
	narrow := opts
	narrow.Width = 80
	narrow.Height = 40
	lines, _, _ = AnalyticsDashboardLines(narrow)
	if len(lines) != 6*16+5 {
		t.Fatalf("single-column rows = %d, want %d", len(lines), 6*16+5)
	}
}

// TestAnalyticsDashboardTruncatesOverflowLikeRust covers the "… z to maximize"
// hint (Rust #45770 removed the in-card group picker).
func TestAnalyticsDashboardTruncatesOverflowLikeRust(t *testing.T) {
	visible := []AnalyticsSection{AnalyticsSectionUsage}
	long := make([]string, 30)
	for index := range long {
		long[index] = "line"
	}
	options := AnalyticsDashboardOptions{
		Width:    60,
		Height:   30,
		Visible:  visible,
		Section:  AnalyticsSectionUsage,
		Business: false,
		History:  func(section AnalyticsSection, width, height int) []string { return long },
	}
	lines, _, _ := AnalyticsDashboardLines(options)
	text := strings.Join(lines, "\n")
	if !strings.Contains(text, "… z to maximize") {
		t.Fatalf("overflow hint missing:\n%s", text)
	}
}

// TestAnalyticsPanelContainsChartLikeRust covers the maximized panel heading,
// grouping, business model line, and its full selection range.
func TestAnalyticsPanelContainsChartLikeRust(t *testing.T) {
	history := &AccountAnalyticsHistory{
		Unit: AccountAnalyticsUnitCredits,
		Data: []AccountAnalyticsDay{
			{
				Date:   time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC),
				Total:  20,
				Values: []AccountAnalyticsValue{{Key: "a", Label: "a", Value: 20}},
			},
		},
	}
	state := DefaultAnalyticsSectionState()
	state.Cursor = 6
	state.History = readyAnalyticsLoad(*history)
	panel := AnalyticsPanelLines(AnalyticsPanelOptions{
		Section:             AnalyticsSectionUsage,
		State:               state,
		History:             history,
		Width:               44,
		ChartHeight:         4,
		RangeIndex:          0,
		EndDate:             time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC),
		Focused:             true,
		Business:            true,
		ConsumerAttribution: false,
		TokenModel:          strPtrForTest("gpt-5"),
	})
	text := strings.Join(panel.Lines, "\n")
	if !strings.Contains(text, "Token usage history") || !strings.Contains(text, "By product") {
		t.Fatalf("panel heading:\n%s", text)
	}
	if !strings.Contains(text, "gpt-5 · m change model") {
		t.Fatalf("business model line missing:\n%s", text)
	}
	if panel.SelectionStart != 0 || panel.SelectionEnd != len(panel.Lines) {
		t.Fatalf("panel selection = (%d,%d), want (0,%d)", panel.SelectionStart, panel.SelectionEnd, len(panel.Lines))
	}
	if panel.ChartBands != 1 {
		t.Fatalf("chart bands = %d, want 1", panel.ChartBands)
	}
}

func strPtrForTest(value string) *string { return &value }

func readyAnalyticsLoad[T any](value T) AnalyticsLoad[T] {
	var load AnalyticsLoad[T]
	load.state = AnalyticsLoadStateReady
	load.value = value
	return load
}

// TestAnalyticsLoadInvalidateMatchesRust covers closing or refreshing the
// dashboard, which drops every account-owned report back to Unavailable.
func TestAnalyticsLoadInvalidateMatchesRust(t *testing.T) {
	load := UnavailableAnalyticsLoad[int]()
	if load.State() != AnalyticsLoadStateUnavailable {
		t.Fatalf("state = %v, want unavailable", load.State())
	}
	if _, ok := load.Message(); !ok {
		t.Fatal("unavailable load should expose its placeholder message")
	}
	ready := readyAnalyticsLoad(7)
	ready.Invalidate()
	if ready.State() != AnalyticsLoadStateUnavailable {
		t.Fatalf("invalidated state = %v, want unavailable", ready.State())
	}
	if _, ok := ready.Ready(); ok {
		t.Fatal("invalidated load kept its value")
	}
}

// TestAnalyticsHistoryLinesRenderBreakdownsLikeRust covers totals, shares, the
// remainder row, expansion, relative units, and empty breakdowns.
func TestAnalyticsHistoryLinesRenderBreakdownsLikeRust(t *testing.T) {
	end := time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC)
	state := DefaultAnalyticsSectionState()
	history := &AccountAnalyticsHistory{
		Unit: AccountAnalyticsUnitCredits,
		Data: []AccountAnalyticsDay{{
			Date:  end,
			Total: 20,
			Values: []AccountAnalyticsValue{
				{Key: "a", Label: "a", Value: 8},
				{Key: "b", Label: "b", Value: 4},
				{Key: "c", Label: "c", Value: 3},
				{Key: "d", Label: "d", Value: 2},
				{Key: "e", Label: "e", Value: 3},
			},
		}},
	}
	opts := AnalyticsHistoryOptions{Width: 44, ChartHeight: 4, RangeIndex: 0, EndDate: end, Zoomed: true}
	lines, bands := AnalyticsHistoryLines(AnalyticsSectionUsage, state, history, opts)
	text := strings.Join(lines, "\n")
	if !strings.Contains(text, "20.00 credits") {
		t.Fatalf("total missing:\n%s", text)
	}
	if !strings.Contains(text, "Jan 15 · 20.00 credits") {
		t.Fatalf("selected-day summary missing:\n%s", text)
	}
	if !strings.Contains(text, "40.0%") {
		t.Fatalf("usage share missing:\n%s", text)
	}
	if !strings.Contains(text, "2 more") {
		t.Fatalf("remainder row missing:\n%s", text)
	}
	if bands != 1 {
		t.Fatalf("bands = %d, want 1", bands)
	}

	detail := 6
	state.Detail = &detail
	lines, _ = AnalyticsHistoryLines(AnalyticsSectionUsage, state, history, opts)
	text = strings.Join(lines, "\n")
	if strings.Contains(text, "more") {
		t.Fatalf("expanded table kept the remainder row:\n%s", text)
	}
	for _, key := range []string{"a", "b", "c", "d", "e"} {
		if !strings.Contains(text, "● "+key) {
			t.Fatalf("expanded table missing %q:\n%s", key, text)
		}
	}

	relative := &AccountAnalyticsHistory{
		Unit: AccountAnalyticsUnitRelativeUsage,
		Data: []AccountAnalyticsDay{{Date: end, Total: 0.5, Values: []AccountAnalyticsValue{{Key: "a", Label: "a", Value: 0.5}}}},
	}
	state.Detail = nil
	lines, _ = AnalyticsHistoryLines(AnalyticsSectionUsage, state, relative, opts)
	text = strings.Join(lines, "\n")
	if !strings.Contains(text, "Total usage · relative units") || !strings.Contains(text, "0.5 usage units") {
		t.Fatalf("relative usage lines:\n%s", text)
	}

	plugins := &AccountAnalyticsHistory{
		Unit: AccountAnalyticsUnitCount,
		Data: []AccountAnalyticsDay{{Date: end, Total: 0}},
	}
	lines, _ = AnalyticsHistoryLines(AnalyticsSectionPlugins, state, plugins, opts)
	text = strings.Join(lines, "\n")
	if !strings.Contains(text, "No calls reported for Jan 15.") {
		t.Fatalf("empty breakdown message:\n%s", text)
	}
}

// TestAnalyticsHistoryLinesMessageStatesLikeRust covers the load placeholders.
func TestAnalyticsHistoryLinesMessageStatesLikeRust(t *testing.T) {
	opts := AnalyticsHistoryOptions{Width: 44, ChartHeight: 4, RangeIndex: 0, EndDate: time.Now()}
	state := DefaultAnalyticsSectionState()
	lines, bands := AnalyticsHistoryLines(AnalyticsSectionUsage, state, nil, opts)
	if len(lines) != 1 || lines[0] != "No history has been reported." || bands != 0 {
		t.Fatalf("unavailable lines = %v, bands = %d", lines, bands)
	}
	empty := &AccountAnalyticsHistory{}
	lines, _ = AnalyticsHistoryLines(AnalyticsSectionUsage, state, empty, opts)
	if len(lines) != 1 || lines[0] != "No data reported for this range." {
		t.Fatalf("empty history lines = %v", lines)
	}
}

// TestAnalyticsHistoryLinesCompactLargeTotalsLikeRust mirrors Rust #45769's
// chart.rs formatted_total rule: totals at or above 1000 switch to the compact
// form while smaller totals keep the plain amount.
func TestAnalyticsHistoryLinesCompactLargeTotalsLikeRust(t *testing.T) {
	end := time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC)
	state := DefaultAnalyticsSectionState()
	opts := AnalyticsHistoryOptions{Width: 44, ChartHeight: 4, RangeIndex: 0, EndDate: end, Zoomed: true}
	for _, test := range []struct {
		total float64
		unit  AccountAnalyticsUnit
		want  string
	}{
		{total: 1_500_000, unit: AccountAnalyticsUnitTokens, want: "1.5M tokens"},
		{total: 999, unit: AccountAnalyticsUnitTokens, want: "999 tokens"},
		{total: 1_000, unit: AccountAnalyticsUnitCredits, want: "1K credits"},
		{total: 12_500, unit: AccountAnalyticsUnitCredits, want: "12.5K credits"},
	} {
		history := &AccountAnalyticsHistory{
			Unit: test.unit,
			Data: []AccountAnalyticsDay{{
				Date:   end,
				Total:  test.total,
				Values: []AccountAnalyticsValue{{Key: "a", Label: "a", Value: test.total}},
			}},
		}
		lines, _ := AnalyticsHistoryLines(AnalyticsSectionUsage, state, history, opts)
		if len(lines) == 0 || lines[0] != test.want {
			t.Fatalf("total %v headline = %q, want %q", test.total, lines, test.want)
		}
	}
}
