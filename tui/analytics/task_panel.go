package analytics

import (
	"fmt"
	"sort"
	"strings"

	"codex_go/chatgptapi"
)

// Consumer chat metrics retain actual balance debits, current-limit
// comparisons, and partial states (Rust analytics/task_panel.rs, #45768).

// AnalyticsTaskMetricNames labels the three consumer sort metrics in cycling
// order.
var AnalyticsTaskMetricNames = [3]string{"Weekly %", "5-hour %", "Balance credits"}

const (
	analyticsTaskCoverage        = "Local chats · includes discovered descendants · excludes archived roots"
	analyticsTaskTruncatedNote   = "Partial ranking · 100 most recently active chats"
	analyticsTaskUnavailableNote = "  Task usage unavailable."
)

// AnalyticsTaskPanelOptions describes the maximized consumer Top chats table.
type AnalyticsTaskPanelOptions struct {
	Chats *AnalyticsTaskChats
	// Message is the load placeholder or error while Ready is false.
	Message string
	Ready   bool
	Cursor  int
	Detail  *int
	// Metric is the active sort index; unknown indices fall back to the first
	// available metric.
	Metric         int
	Width          int
	Zoomed         bool
	ViewportHeight int
	// ModelName maps a model key onto its display name; nil keeps the key.
	ModelName func(string) string
}

// AnalyticsTaskPanel is the table content plus the selection range of the
// cursor row.
type AnalyticsTaskPanel struct {
	Lines          []string
	SelectionStart int
	SelectionEnd   int
}

// AnalyticsTaskMetrics lists the metrics that have data anywhere in the
// ranking; balance credits are always offered (Rust task_metrics).
func AnalyticsTaskMetrics(chats *AnalyticsTaskChats) []int {
	metrics := make([]int, 0, 3)
	for _, metric := range []int{0, 1} {
		if chats == nil {
			break
		}
		for _, chat := range chats.Rows {
			amounts := AnalyticsTaskAvailable(chat)
			if amounts == nil {
				continue
			}
			value := amounts.WeeklyLimitPercent
			if metric == 1 {
				value = amounts.FiveHourLimitPercent
			}
			if value != nil {
				metrics = append(metrics, metric)
				break
			}
		}
	}
	return append(metrics, 2)
}

// AnalyticsTaskMetric clamps a remembered metric onto the available set.
func AnalyticsTaskMetric(metric int, chats *AnalyticsTaskChats) int {
	available := AnalyticsTaskMetrics(chats)
	for _, candidate := range available {
		if candidate == metric {
			return metric
		}
	}
	return available[0]
}

// AnalyticsTaskAvailable exposes the available amounts of a row, or nil when
// its accounting is missing or unavailable.
func AnalyticsTaskAvailable(chat AnalyticsTaskChat) *chatgptapi.TaskUsageAmounts {
	if chat.Task == nil || chat.Task.DataStatus == chatgptapi.TaskUsageStatusUnavailable {
		return nil
	}
	return &chat.Task.Amounts
}

// AnalyticsTaskRows ranks the loaded rows by the active metric, keeping
// missing values last (Rust task_rows).
func AnalyticsTaskRows(chats *AnalyticsTaskChats, metric int) []AnalyticsTaskChat {
	if chats == nil {
		return nil
	}
	rows := make([]AnalyticsTaskChat, len(chats.Rows))
	copy(rows, chats.Rows)
	metric = AnalyticsTaskMetric(metric, chats)
	sort.SliceStable(rows, func(left, right int) bool {
		return analyticsTaskCompare(
			AnalyticsTaskAvailable(rows[right]),
			AnalyticsTaskAvailable(rows[left]),
			metric,
		) < 0
	})
	return rows
}

// analyticsTaskCompare orders two amount sets for one metric; nil sorts below
// every value.
func analyticsTaskCompare(left, right *chatgptapi.TaskUsageAmounts, metric int) int {
	switch metric {
	case 2:
		leftCredits := analyticsTaskCredits(left)
		rightCredits := analyticsTaskCredits(right)
		switch {
		case leftCredits == nil && rightCredits == nil:
			return 0
		case leftCredits == nil:
			return -1
		case rightCredits == nil:
			return 1
		default:
			return leftCredits.Compare(*rightCredits)
		}
	default:
		leftValue := analyticsTaskPercent(left, metric)
		rightValue := analyticsTaskPercent(right, metric)
		switch {
		case leftValue == nil && rightValue == nil:
			return 0
		case leftValue == nil:
			return -1
		case rightValue == nil:
			return 1
		case *leftValue < *rightValue:
			return -1
		case *leftValue > *rightValue:
			return 1
		default:
			return 0
		}
	}
}

func analyticsTaskCredits(amounts *chatgptapi.TaskUsageAmounts) *chatgptapi.TaskUsageCredits {
	if amounts == nil {
		return nil
	}
	return amounts.BalanceUsageCredits
}

func analyticsTaskPercent(amounts *chatgptapi.TaskUsageAmounts, metric int) *float64 {
	if amounts == nil {
		return nil
	}
	if metric == 0 {
		return amounts.WeeklyLimitPercent
	}
	return amounts.FiveHourLimitPercent
}

// analyticsTaskAmount renders one metric, keeping missing values distinct from
// zero.
func analyticsTaskAmount(amounts *chatgptapi.TaskUsageAmounts, metric int) string {
	if amounts == nil {
		return "—"
	}
	if metric == 2 {
		if amounts.BalanceUsageCredits == nil {
			return "—"
		}
		return amounts.BalanceUsageCredits.String()
	}
	value := analyticsTaskPercent(amounts, metric)
	if value == nil {
		return "—"
	}
	return fmt.Sprintf("%.1f%%", *value)
}

// AnalyticsTaskPanelLines renders the consumer Top chats table
// (Rust AnalyticsView::task_lines).
func AnalyticsTaskPanelLines(opts AnalyticsTaskPanelOptions) AnalyticsTaskPanel {
	width := opts.Width
	if width < 1 {
		width = 1
	}
	if width > 110 {
		width = 110
	}
	wrap := func(lines []string) []string { return analyticsWrapLines(lines, width) }
	if !opts.Ready {
		message := opts.Message
		if message == "" {
			message = "No task usage reported."
		}
		return AnalyticsTaskPanel{Lines: wrap([]string{message}), SelectionEnd: 1}
	}
	chats := opts.Chats
	metric := AnalyticsTaskMetric(opts.Metric, chats)
	lines := wrap([]string{
		"Usage estimates · active in past 30 days · sorted by " + AnalyticsTaskMetricNames[metric],
		analyticsTaskSubtitle(metric),
	})
	if chats == nil || len(chats.Rows) == 0 {
		lines = append(lines, "No recent local chats.")
		return AnalyticsTaskPanel{Lines: lines, SelectionEnd: 1}
	}
	missing := 0
	partial := 0
	for _, chat := range chats.Rows {
		if analyticsTaskAmount(AnalyticsTaskAvailable(chat), metric) == "—" {
			missing++
		}
		if chat.Task != nil && chat.Task.DataStatus == chatgptapi.TaskUsageStatusPartial {
			partial++
		}
	}
	if missing+partial > 0 {
		lines = append(lines, wrap([]string{
			fmt.Sprintf("Partial ranking · %d partial · %d unavailable", partial, missing),
		})...)
	}
	metrics := AnalyticsTaskMetrics(chats)
	widths := make([]int, len(metrics))
	for index, candidate := range metrics {
		width := len(AnalyticsTaskMetricNames[candidate])
		for _, chat := range chats.Rows {
			if length := analyticsDisplayWidth(analyticsTaskAmount(AnalyticsTaskAvailable(chat), candidate)); length > width {
				width = length
			}
		}
		widths[index] = width
	}
	total := len(metrics) * 2
	for _, columnWidth := range widths {
		total += columnWidth
	}
	showAll := total+24 <= width
	metricText := func(chat *AnalyticsTaskChat) string {
		if !showAll {
			if chat == nil {
				return AnalyticsTaskMetricNames[metric]
			}
			return analyticsTaskAmount(AnalyticsTaskAvailable(*chat), metric)
		}
		parts := make([]string, 0, len(metrics))
		for index, candidate := range metrics {
			value := AnalyticsTaskMetricNames[candidate]
			if chat != nil {
				value = analyticsTaskAmount(AnalyticsTaskAvailable(*chat), candidate)
			}
			parts = append(parts, fmt.Sprintf("%*s", widths[index], value))
		}
		return strings.Join(parts, "  ")
	}
	lines = append(lines, "")
	lines = append(lines, analyticsColumns("  Chat", metricText(nil), width))
	rows := make([][]string, 0, len(chats.Rows))
	for index, chat := range AnalyticsTaskRows(chats, metric) {
		selected := index == opts.Cursor
		value := metricText(&chat)
		status := ""
		if chat.Task != nil && chat.Task.DataStatus == chatgptapi.TaskUsageStatusPartial {
			status = " · partial"
		}
		marker := "  "
		if selected {
			marker = "› "
		}
		label := marker + chat.DisplayTitle() + status
		labelWidth := width - (analyticsDisplayWidth(value) + 1)
		if labelWidth < 0 {
			labelWidth = 0
		}
		row := []string{analyticsColumns(analyticsTruncate(label, labelWidth), value, width)}
		if opts.Zoomed && opts.Detail != nil && *opts.Detail == index {
			row = append(row, analyticsTaskDetailLines(chat, opts, metric, width)...)
		}
		rows = append(rows, wrap(row))
	}
	coverage := make([]string, 0, 3)
	coverage = append(coverage, analyticsTaskCoverage)
	if chats.Truncated {
		coverage = append(coverage, analyticsTaskTruncatedNote)
	}
	if chats.UpdatedAt != nil {
		coverage = append(coverage, fmt.Sprintf("Updated %s UTC · recent activity may be delayed",
			chats.UpdatedAt.UTC().Format("Jan 2 15:04")))
	}
	wrappedCoverage := wrap(coverage)
	if !opts.Zoomed {
		count := len(rows)
		limit := count
		if limit > 5 {
			limit = 5
		}
		for _, row := range rows[:limit] {
			lines = append(lines, row...)
		}
		lines = append(lines, "")
		lines = append(lines, fmt.Sprintf("Showing top %d of %d chats", limit, count))
		lines = append(lines, wrappedCoverage...)
		return AnalyticsTaskPanel{Lines: lines, SelectionEnd: 1}
	}
	window := analyticsChatRowWindow(lines, rows, wrappedCoverage, width, AnalyticsChatPanelOptions{
		Cursor:         opts.Cursor,
		Zoomed:         true,
		ViewportHeight: opts.ViewportHeight,
	})
	return AnalyticsTaskPanel{Lines: window.Lines, SelectionStart: window.SelectionStart, SelectionEnd: window.SelectionEnd}
}

func analyticsTaskSubtitle(metric int) string {
	if metric == 2 {
		return "Credits debited from balance · includes adjustments"
	}
	return "Recorded usage / current full limit · may exceed 100%"
}

// analyticsTaskDetailLines renders the expanded breakdown of one chat.
func analyticsTaskDetailLines(chat AnalyticsTaskChat, opts AnalyticsTaskPanelOptions, metric, width int) []string {
	row := []string{strings.Repeat("─", width)}
	row = append(row, analyticsWrap("  "+chat.DisplayTitle(), width)...)
	for _, candidate := range AnalyticsTaskMetrics(opts.Chats) {
		row = append(row, analyticsColumns("  "+AnalyticsTaskMetricNames[candidate],
			analyticsTaskAmount(AnalyticsTaskAvailable(chat), candidate), width))
	}
	if chat.Task == nil {
		row = append(row, analyticsTaskUnavailableNote)
		row = append(row, "")
		return row
	}
	if chat.Task.DataStatus != chatgptapi.TaskUsageStatusUnavailable && len(chat.Task.Groups) > 0 {
		groups := make([]chatgptapi.TaskUsageGroup, len(chat.Task.Groups))
		copy(groups, chat.Task.Groups)
		sort.SliceStable(groups, func(left, right int) bool {
			return analyticsTaskCompare(&groups[right].Amounts, &groups[left].Amounts, metric) < 0
		})
		row = append(row, "")
		row = append(row, analyticsColumns("  Model / effort / speed", AnalyticsTaskMetricNames[metric], width))
		for _, group := range groups {
			value := analyticsTaskAmount(&group.Amounts, metric)
			model := "Not reported"
			if group.Model != nil && strings.TrimSpace(*group.Model) != "" {
				model = *group.Model
				if opts.ModelName != nil {
					model = opts.ModelName(model)
				}
			}
			labelWidth := width - (analyticsDisplayWidth(value) + 1)
			if labelWidth < 0 {
				labelWidth = 0
			}
			row = append(row, analyticsColumns(analyticsTruncate("  "+model, labelWidth), value, width))
			row = append(row, fmt.Sprintf("    %s · %s · %s",
				analyticsTaskGroupText(group.ReasoningEffort),
				analyticsTaskGroupText(group.Speed),
				analyticsTaskGroupText(group.ProductExperience)))
		}
	}
	row = append(row, "")
	return row
}

func analyticsTaskGroupText(value *string) string {
	if value == nil || strings.TrimSpace(*value) == "" {
		return "Not reported"
	}
	return *value
}
