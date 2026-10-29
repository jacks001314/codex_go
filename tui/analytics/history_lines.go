package analytics

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"
)

// AnalyticsHistoryOptions configures one dashboard section's chart and
// breakdown table.
type AnalyticsHistoryOptions struct {
	Width       int
	ChartHeight int
	RangeIndex  int
	EndDate     time.Time
	Zoomed      bool
	Business    bool
	// ConsumerAttribution reports whether consumer usage is fully attributed,
	// which changes the label of the first grouping.
	ConsumerAttribution bool
	// ModelName maps a model key onto its display name; nil keeps the key.
	ModelName func(string) string
}

// AnalyticsHistoryLines renders a section's total, chart, selected-day summary,
// and breakdown table (Rust analytics/chart.rs history_lines, #45764). The
// second result is the number of chart bands, used to size the chart to the
// viewport.
func AnalyticsHistoryLines(
	section AnalyticsSection,
	state AnalyticsSectionState,
	history *AccountAnalyticsHistory,
	opts AnalyticsHistoryOptions,
) ([]string, int) {
	if history == nil {
		if message, ok := state.History.Message(); ok {
			return []string{message}, 0
		}
		return nil, 0
	}
	if len(history.Data) == 0 {
		return []string{"No data reported for this range."}, 0
	}
	days := analyticsSectionDays(section, history, opts)
	if len(days) == 0 {
		return []string{"No data reported for this range."}, 0
	}
	cursor := state.Cursor
	if cursor < 0 {
		cursor = 0
	}
	if cursor > len(days)-1 {
		cursor = len(days) - 1
	}
	selectedDate := days[cursor].Date.Format("Jan 2")
	day := days[cursor].Day
	expanded := opts.Zoomed && state.Detail != nil
	categories := AnalyticsCategories(history)
	if section == AnalyticsSectionPlugins || section == AnalyticsSectionSkills {
		for index := range categories {
			categories[index].Label = AnalyticsToolName(categories[index].Label)
		}
	}
	if state.Group == 2 && opts.ModelName != nil {
		for index := range categories {
			categories[index].Label = opts.ModelName(categories[index].Label)
		}
	}
	unit := analyticsSectionUnit(section, history.Unit)
	formatAmount := analyticsAmount
	if history.Unit == AccountAnalyticsUnitCredits {
		formatAmount = analyticsCreditAmount
	}
	relative := history.Unit == AccountAnalyticsUnitRelativeUsage
	emptyBreakdown := (section == AnalyticsSectionPlugins ||
		section == AnalyticsSectionActivity ||
		section == AnalyticsSectionSkills) &&
		(day == nil || (day.Total == 0 && analyticsValuesAllZero(day.Values)))

	var lines []string
	if relative {
		lines = append(lines, "Total usage · relative units")
	} else {
		total := 0.0
		for _, entry := range history.Data {
			total += entry.Total
		}
		reported := ""
		if section == AnalyticsSectionActivity {
			reported = "reported "
		}
		formatted := formatAmount(total)
		if math.Abs(total) >= 1000 {
			formatted = analyticsCompactAmount(total)
		}
		lines = append(lines, formatted+" "+reported+unit)
	}
	if section == AnalyticsSectionUsage && !opts.Business && opts.Zoomed {
		lines = append(lines, "Approximate · may be delayed up to 6 hours")
	}
	if state.Group == 3 {
		lines = append(lines, "All usage by how each turn started")
	}
	hasActivity := false
	for _, entry := range history.Data {
		if entry.Total != 0 || !analyticsValuesAllZero(entry.Values) {
			hasActivity = true
			break
		}
	}
	bands := 0
	if hasActivity {
		chart := AnalyticsPlot(days, categories, cursor, opts.Width, opts.ChartHeight, history.Unit)
		bands = chart.Bands
		if text := AnalyticsChartText(chart); text != "" {
			lines = append(lines, strings.Split(text, "\n")...)
		}
	} else if section == AnalyticsSectionPlugins || section == AnalyticsSectionActivity || section == AnalyticsSectionSkills {
		lines = append(lines, "No "+unit+" reported in this range.")
	} else {
		lines = append(lines, "No activity reported in this range.")
	}

	tableWidth := opts.Width
	if tableWidth > 56 {
		tableWidth = 56
	}
	lines = append(lines, "")
	if opts.Zoomed {
		lines = append(lines, strings.Repeat("─", tableWidth))
	}
	tableStart := len(lines)
	summary := " · Not reported"
	switch {
	case day == nil:
	case day.Total == 0 && analyticsValuesAllZero(day.Values):
		summary = " · No activity (0)"
	case relative:
		summary = " · " + analyticsAmount(day.Total) + " usage units"
	case expanded:
		summary = " · " + strconv.FormatFloat(day.Total, 'f', -1, 64) + " " + unit
	default:
		summary = " · " + formatAmount(day.Total) + " " + unit
	}
	lines = append(lines, analyticsTruncate(selectedDate+summary, opts.Width))
	if opts.Zoomed {
		lines = append(lines, analyticsColumns(
			analyticsBreakdownHeader(section, state.Group, opts.ConsumerAttribution),
			analyticsTableAmountHeader(section, history.Unit),
			tableWidth,
		))
	}
	lines = append(lines, analyticsBreakdownRows(section, state.Group, day, categories, history.Unit, expanded, opts, formatAmount, tableWidth)...)
	if emptyBreakdown {
		tableEnd := len(lines)
		lines = lines[:tableStart]
		message := "No " + unit + " reported for " + selectedDate + "."
		if day == nil {
			message = "Data not reported for " + selectedDate + "."
		}
		lines = append(lines, analyticsTruncate(message, opts.Width))
		for len(lines) < tableEnd {
			lines = append(lines, "")
		}
	}
	if history.UpdatedAt != nil && *history.UpdatedAt > 0 {
		updated := time.Unix(*history.UpdatedAt, 0).UTC()
		lines = append(lines, "Updated "+updated.Format("Jan 2 15:04")+" UTC")
	}
	return lines, bands
}

func analyticsSectionDays(section AnalyticsSection, history *AccountAnalyticsHistory, opts AnalyticsHistoryOptions) []AnalyticsPlotDay {
	start := AnalyticsSectionStartDate(opts.EndDate, opts.RangeIndex)
	end := opts.EndDate
	var days []AnalyticsPlotDay
	for date := start; !date.After(end); date = date.AddDate(0, 0, 1) {
		var day *AccountAnalyticsDay
		for index := range history.Data {
			if analyticsSameDate(history.Data[index].Date, date) {
				day = &history.Data[index]
				break
			}
		}
		days = append(days, AnalyticsPlotDay{Date: date, Day: day})
	}
	return days
}

func analyticsSameDate(left, right time.Time) bool {
	leftYear, leftMonth, leftDay := left.Date()
	rightYear, rightMonth, rightDay := right.Date()
	return leftYear == rightYear && leftMonth == rightMonth && leftDay == rightDay
}

func analyticsSectionUnit(section AnalyticsSection, unit AccountAnalyticsUnit) string {
	switch section {
	case AnalyticsSectionUsage:
		if unit == AccountAnalyticsUnitTokens {
			return "tokens"
		}
		return "credits"
	case AnalyticsSectionActivity:
		return "messages"
	case AnalyticsSectionPlugins:
		return "calls"
	case AnalyticsSectionSkills:
		return "uses"
	default:
		return "credits"
	}
}

func analyticsTableAmountHeader(section AnalyticsSection, unit AccountAnalyticsUnit) string {
	if unit == AccountAnalyticsUnitTokens {
		return "Tokens"
	}
	switch section {
	case AnalyticsSectionUsage:
		return "Share"
	case AnalyticsSectionActivity:
		return "Messages"
	case AnalyticsSectionPlugins:
		return "Calls"
	case AnalyticsSectionSkills:
		return "Uses"
	default:
		return "Credits"
	}
}

func analyticsBreakdownHeader(section AnalyticsSection, group int, consumerAttribution bool) string {
	switch section {
	case AnalyticsSectionPlugins:
		return "Plugin"
	case AnalyticsSectionSkills:
		return "Skill"
	default:
		return section.GroupLabel(group, consumerAttribution)
	}
}

func analyticsValuesAllZero(values []AccountAnalyticsValue) bool {
	for _, value := range values {
		if value.Value != 0 {
			return false
		}
	}
	return true
}

func analyticsBreakdownRows(
	section AnalyticsSection,
	group int,
	day *AccountAnalyticsDay,
	categories []AccountAnalyticsValue,
	unit AccountAnalyticsUnit,
	expanded bool,
	opts AnalyticsHistoryOptions,
	formatAmount func(float64) string,
	tableWidth int,
) []string {
	type row struct {
		series int
		label  string
		value  float64
		more   bool
	}
	rows := make([]row, 0, len(categories))
	for index, category := range categories {
		value := 0.0
		if day != nil {
			for _, entry := range day.Values {
				if entry.Key == category.Key {
					value = entry.Value
					break
				}
			}
		}
		rows = append(rows, row{series: index, label: category.Label, value: value})
	}
	sort.SliceStable(rows, func(left, right int) bool {
		return math.Abs(rows[left].value) > math.Abs(rows[right].value)
	})
	if !expanded && len(rows) > 4 {
		remaining := rows[3:]
		sum := 0.0
		for _, entry := range remaining {
			sum += entry.value
		}
		rows = append(rows[:3], row{label: strconv.Itoa(len(remaining)) + " more", value: sum, more: true})
	}
	limit := len(rows)
	if !opts.Zoomed && limit > 2 {
		limit = 2
	}
	lines := make([]string, 0, limit)
	for _, entry := range rows[:limit] {
		marker := "● "
		if entry.more {
			marker = "… "
		}
		amount := "—"
		switch {
		case day == nil:
		case section == AnalyticsSectionUsage && unit != AccountAnalyticsUnitTokens:
			share := 0.0
			if day.Total != 0 {
				share = entry.value / day.Total * 100
			}
			amount = fmt.Sprintf("%.1f%%", share)
		case expanded:
			amount = strconv.FormatFloat(entry.value, 'f', -1, 64)
		default:
			amount = formatAmount(entry.value)
		}
		labelLimit := tableWidth - len(amount) - 2
		if labelLimit < 0 {
			labelLimit = 0
		}
		lines = append(lines, analyticsColumns(
			analyticsTruncate(marker+entry.label, labelLimit),
			amount,
			tableWidth,
		))
	}
	return lines
}
