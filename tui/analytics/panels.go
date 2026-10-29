package analytics

import (
	"strconv"
	"strings"
	"time"

	"codex_go/auth"
	"codex_go/chatgptapi"
)

// AnalyticsPanelOptions describes the maximized section panel.
type AnalyticsPanelOptions struct {
	Section AnalyticsSection
	State   AnalyticsSectionState
	History *AccountAnalyticsHistory
	// Chats supplies the Top chats rows; the section never loads a history
	// report (Rust #45765).
	Chats       *AnalyticsChats
	ChatMessage string
	ChatReady   bool
	// Tasks supplies the consumer Top chats rows; the section renders current
	// allowance and balance debits instead of lifetime estimates (Rust #45768).
	Tasks       *AnalyticsTaskChats
	TaskMessage string
	TaskReady   bool
	ChatMetric  int
	// Profile supplies the account Summary (Rust #45769); Plan labels its
	// subscription row.
	Profile              *chatgptapi.AccountProfile
	ProfileMessage       string
	ProfileReady         bool
	Plan                 *auth.PlanType
	ShowZeroCreditGroups bool
	ViewportHeight       int
	Width                int
	ChartHeight          int
	RangeIndex           int
	EndDate              time.Time
	Focused              bool
	Business             bool
	ConsumerAttribution  bool
	TokenModel           *string
	ModelName            func(string) string
}

// AnalyticsPanel is wrapped panel content plus the measured selection range and
// chart geometry (Rust analytics/panels.rs Panel).
type AnalyticsPanel struct {
	Lines          []string
	SelectionStart int
	SelectionEnd   int
	ChartBands     int
}

// AnalyticsPanelLines builds the maximized section: heading, grouping, and
// the history chart with its breakdown table.
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
	bands := 0
	selectionStart := 0
	selectionEnd := len(lines)
	if opts.Section == AnalyticsSectionSummary {
		// Summary keeps the heading selectable and scrolls its profile body
		// (Rust panels.rs Summary branch, #45769).
		selectionStart, selectionEnd = 0, 1
		lines = append(lines, AnalyticsSummaryLines(AnalyticsSummaryOptions{
			Profile: opts.Profile,
			Message: opts.ProfileMessage,
			Ready:   opts.ProfileReady,
			Plan:    opts.Plan,
			Zoomed:  true,
			View:    opts.State.Group,
			EndDate: opts.EndDate,
			Width:   width,
		})...)
	} else if opts.Section == AnalyticsSectionChats {
		var panel AnalyticsChatPanel
		if opts.Business {
			panel = AnalyticsChatPanelLines(AnalyticsChatPanelOptions{
				Chats:                opts.Chats,
				Message:              opts.ChatMessage,
				Ready:                opts.ChatReady,
				Cursor:               opts.State.Cursor,
				Detail:               opts.State.Detail,
				ShowZeroCreditGroups: opts.ShowZeroCreditGroups,
				Width:                width,
				Zoomed:               true,
				ViewportHeight:       opts.ViewportHeight,
				ModelName:            opts.ModelName,
			})
		} else {
			tasks := AnalyticsTaskPanelLines(AnalyticsTaskPanelOptions{
				Chats:          opts.Tasks,
				Message:        opts.TaskMessage,
				Ready:          opts.TaskReady,
				Cursor:         opts.State.Cursor,
				Detail:         opts.State.Detail,
				Metric:         opts.ChatMetric,
				Width:          width,
				Zoomed:         true,
				ViewportHeight: opts.ViewportHeight,
				ModelName:      opts.ModelName,
			})
			panel = AnalyticsChatPanel{
				Lines:          tasks.Lines,
				SelectionStart: tasks.SelectionStart,
				SelectionEnd:   tasks.SelectionEnd,
			}
		}
		selectionStart = len(lines) + panel.SelectionStart
		selectionEnd = len(lines) + panel.SelectionEnd
		lines = append(lines, panel.Lines...)
	} else {
		historyLines, chartBands := AnalyticsHistoryLines(opts.Section, opts.State, opts.History, AnalyticsHistoryOptions{
			Width:               width,
			ChartHeight:         opts.ChartHeight,
			RangeIndex:          opts.RangeIndex,
			EndDate:             opts.EndDate,
			Zoomed:              true,
			Business:            opts.Business,
			ConsumerAttribution: opts.ConsumerAttribution,
			ModelName:           opts.ModelName,
		})
		bands = chartBands
		lines = append(lines, historyLines...)
		selectionEnd = len(lines)
	}
	wrapped := make([]string, 0, len(lines))
	wrappedStart, wrappedEnd := 0, 0
	for index, line := range lines {
		if index == selectionStart {
			wrappedStart = len(wrapped)
		}
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
// The load carries the account plan so the layout can gate plan-specific
// sections such as Top chats.
func AnalyticsStatusLine(account AnalyticsLoad[auth.PlanType]) string {
	if message, ok := account.Message(); ok {
		return message
	}
	return "Analytics is not available for this account type."
}

// AnalyticsReportRangeLabel is the "7d"/"30d" label for one range index.
func AnalyticsReportRangeLabel(rangeIndex int) string {
	return strconv.Itoa(AnalyticsSectionDays(rangeIndex)) + "d"
}

// AnalyticsDayHasDetails reports whether one day of the selected range carries
// a nonzero breakdown value, which is what enables inline details for the tool
// and activity sections (Rust AnalyticsView::day_has_details).
func AnalyticsDayHasDetails(history *AccountAnalyticsHistory, start time.Time, dayIndex int) bool {
	if history == nil || dayIndex < 0 {
		return false
	}
	date := start.AddDate(0, 0, dayIndex)
	for _, day := range history.Data {
		if day.Date.Format("2006-01-02") != date.Format("2006-01-02") {
			continue
		}
		for _, value := range day.Values {
			if value.Value != 0 {
				return true
			}
		}
	}
	return false
}
