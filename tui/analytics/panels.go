package analytics

import (
	"strconv"
	"strings"
	"time"
)

// AnalyticsPanelOptions describes the maximized section panel.
type AnalyticsPanelOptions struct {
	Section             AnalyticsSection
	State               AnalyticsSectionState
	History             *AccountAnalyticsHistory
	Width               int
	ChartHeight         int
	RangeIndex          int
	EndDate             time.Time
	Focused             bool
	Business            bool
	ConsumerAttribution bool
	TokenModel          *string
	GroupOptions        []int
	GroupPicker         *int
	ModelName           func(string) string
}

// AnalyticsPanel is wrapped panel content plus the measured selection range and
// chart geometry (Rust analytics/panels.rs Panel).
type AnalyticsPanel struct {
	Lines          []string
	SelectionStart int
	SelectionEnd   int
	ChartBands     int
}

// AnalyticsPanelLines builds the maximized section: heading, grouping, optional
// group picker, and the history chart with its breakdown table.
func AnalyticsPanelLines(opts AnalyticsPanelOptions) AnalyticsPanel {
	width := opts.Width
	if width < 1 {
		width = 1
	}
	title := "  " + AnalyticsSectionTitle(opts.Section, opts.Business, opts.ConsumerAttribution)
	if opts.Focused {
		title = "▎ " + AnalyticsSectionTitle(opts.Section, opts.Business, opts.ConsumerAttribution)
	}
	heading := title
	if opts.Section == AnalyticsSectionUsage || opts.Section == AnalyticsSectionCredits || opts.Section == AnalyticsSectionActivity {
		heading = analyticsColumns(heading, "By "+strings.ToLower(opts.Section.GroupLabel(opts.State.Group, opts.ConsumerAttribution)), width)
	}
	lines := []string{heading, strings.Repeat("─", width)}
	if opts.Section == AnalyticsSectionUsage && opts.Business {
		model := "All models"
		if opts.TokenModel != nil && *opts.TokenModel != "" {
			model = *opts.TokenModel
		}
		lines = append(lines, model+" · m change model")
	}
	if opts.Focused && opts.GroupPicker != nil {
		for index, group := range opts.GroupOptions {
			marker := " "
			if index == *opts.GroupPicker {
				marker = "›"
			}
			lines = append(lines, marker+" "+opts.Section.GroupLabel(group, opts.ConsumerAttribution))
		}
	}
	historyLines, bands := AnalyticsHistoryLines(opts.Section, opts.State, opts.History, AnalyticsHistoryOptions{
		Width:               width,
		ChartHeight:         opts.ChartHeight,
		RangeIndex:          opts.RangeIndex,
		EndDate:             opts.EndDate,
		Zoomed:              true,
		Business:            opts.Business,
		ConsumerAttribution: opts.ConsumerAttribution,
		ModelName:           opts.ModelName,
	})
	lines = append(lines, historyLines...)
	selectionEnd := len(lines)
	if opts.Focused && opts.GroupPicker != nil {
		selectionEnd = 2 + len(opts.GroupOptions)
	}
	wrapped := make([]string, 0, len(lines))
	wrappedStart, wrappedEnd := 0, 0
	for index, line := range lines {
		wrapped = append(wrapped, analyticsWrap(line, width)...)
		if index+1 == selectionEnd {
			wrappedEnd = len(wrapped)
		}
	}
	return AnalyticsPanel{
		Lines:          wrapped,
		SelectionStart: wrappedStart,
		SelectionEnd:   wrappedEnd,
		ChartBands:     bands,
	}
}

// AnalyticsStatusLine is the panel/account status text used when the account is
// still loading or unavailable (Rust AnalyticsView::render account branch).
func AnalyticsStatusLine(account AnalyticsLoad[AccountKind]) string {
	if message, ok := account.Message(); ok {
		return message
	}
	return "Analytics is not available for this account type."
}

// AnalyticsReportRangeLabel is the "7d"/"30d" label for one range index.
func AnalyticsReportRangeLabel(rangeIndex int) string {
	return strconv.Itoa(AnalyticsSectionDays(rangeIndex)) + "d"
}
