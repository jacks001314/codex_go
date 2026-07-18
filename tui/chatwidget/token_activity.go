package chatwidget

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	codextui "codex_go/tui"
)

const (
	tokenActivityWeekCount      = 52
	tokenActivityDayCount       = 7
	tokenActivityCellCount      = tokenActivityWeekCount * tokenActivityDayCount
	tokenActivityChartLeftWidth = 4

	tokenActivityEmptyCellGlyph  = "\u25a1"
	tokenActivityActiveCellGlyph = "\u25a0"
	tokenActivityBarCellGlyph    = "\u2588"
	tokenActivitySeparator       = " \u00b7 "
)

type TokenActivityView string

const (
	TokenActivityDaily      TokenActivityView = "daily"
	TokenActivityWeekly     TokenActivityView = "weekly"
	TokenActivityCumulative TokenActivityView = "cumulative"
)

func ParseTokenActivityView(value string) (TokenActivityView, bool) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "day", "daily":
		return TokenActivityDaily, true
	case "week", "weekly":
		return TokenActivityWeekly, true
	case "cumulative":
		return TokenActivityCumulative, true
	default:
		return "", false
	}
}

func (v TokenActivityView) Label() string {
	switch v {
	case TokenActivityWeekly:
		return "Weekly"
	case TokenActivityCumulative:
		return "Cumulative"
	default:
		return "Daily"
	}
}

type TokenActivitySummary struct {
	LifetimeTokens        *int64
	PeakDailyTokens       *int64
	LongestRunningTurnSec *int64
	CurrentStreakDays     *int64
	LongestStreakDays     *int64
}

type TokenActivityDailyBucket struct {
	StartDate string
	Tokens    int64
}

type TokenActivityResponse struct {
	Summary           TokenActivitySummary
	DailyUsageBuckets *[]TokenActivityDailyBucket
}

type TokenActivityStateKind string

const (
	TokenActivityLoading TokenActivityStateKind = "loading"
	TokenActivityLoaded  TokenActivityStateKind = "loaded"
	TokenActivityError   TokenActivityStateKind = "error"
)

type TokenActivityState struct {
	Kind     TokenActivityStateKind
	Response *TokenActivityResponse
	Today    time.Time
}

func NewTokenActivityLoadingState() TokenActivityState {
	return TokenActivityState{Kind: TokenActivityLoading}
}

func NewTokenActivityLoadedState(response TokenActivityResponse, today time.Time) TokenActivityState {
	return TokenActivityState{Kind: TokenActivityLoaded, Response: &response, Today: today}
}

func NewTokenActivityErrorState() TokenActivityState {
	return TokenActivityState{Kind: TokenActivityError}
}

func TokenActivityLines(view TokenActivityView, state TokenActivityState, width int) []string {
	switch state.Kind {
	case TokenActivityLoaded:
		if state.Response == nil {
			return tokenActivityUnavailableLines()
		}
		lines := []string{" Token activity   last 12 months"}
		lines = append(lines, TokenActivitySummaryLines(state.Response.Summary, graphWidth(width))...)
		lines = append(lines, "")
		if state.Response.DailyUsageBuckets == nil {
			lines = append(lines, "   Token activity history unavailable")
			return lines
		}
		lines = append(lines, TokenActivityChartLines(view, *state.Response.DailyUsageBuckets, state.Today, width)...)
		return lines
	case TokenActivityError:
		return tokenActivityUnavailableLines()
	default:
		return []string{" Token activity", "   Loading..."}
	}
}

func tokenActivityUnavailableLines() []string {
	return []string{" Token activity", "   Token activity unavailable"}
}

func TokenActivitySummaryLines(summary TokenActivitySummary, width int) []string {
	fields := []summaryField{
		{Label: "Lifetime", Value: FormatTokensCompactPtr(summary.LifetimeTokens)},
		{Label: "Peak", Value: FormatTokensCompactPtr(summary.PeakDailyTokens)},
		{Label: "Streak", Value: FormatStreak(summary.CurrentStreakDays, summary.LongestStreakDays)},
		{Label: "Longest task", Value: FormatOptionalDuration(summary.LongestRunningTurnSec)},
	}
	groups := packSummaryFields(fields, width)
	out := make([]string, 0, len(groups))
	for _, group := range groups {
		out = append(out, " "+summaryLine(fields, group))
	}
	return out
}

func FormatTokensCompactPtr(value *int64) string {
	if value == nil {
		return "-"
	}
	return FormatTokensCompact(*value)
}

func FormatTokensCompact(value int64) string {
	if value < 0 {
		value = 0
	}
	if value == 0 {
		return "0"
	}
	if value < 1000 {
		return fmt.Sprintf("%d", value)
	}
	scaled := float64(value)
	suffix := "K"
	switch {
	case value >= 1_000_000_000_000:
		scaled = scaled / 1_000_000_000_000
		suffix = "T"
	case value >= 1_000_000_000:
		scaled = scaled / 1_000_000_000
		suffix = "B"
	case value >= 1_000_000:
		scaled = scaled / 1_000_000
		suffix = "M"
	default:
		scaled = scaled / 1_000
	}
	decimals := 0
	switch {
	case scaled < 10:
		decimals = 2
	case scaled < 100:
		decimals = 1
	}
	formatted := strconvFormatFloat(scaled, decimals)
	return formatted + suffix
}

func FormatStreak(current *int64, longest *int64) string {
	switch {
	case current != nil && longest != nil && *current == *longest:
		return fmt.Sprintf("%dd", *current)
	case current != nil && longest != nil:
		return fmt.Sprintf("%dd (best %dd)", *current, *longest)
	case current != nil:
		return fmt.Sprintf("%dd", *current)
	case longest != nil:
		return fmt.Sprintf("- (best %dd)", *longest)
	default:
		return "-"
	}
}

func FormatOptionalDuration(seconds *int64) string {
	if seconds == nil {
		return "-"
	}
	value := *seconds
	if value < 0 {
		value = 0
	}
	hours := value / 3600
	minutes := (value % 3600) / 60
	switch {
	case hours == 0 && minutes == 0:
		return fmt.Sprintf("%ds", value)
	case hours == 0:
		return fmt.Sprintf("%dm", minutes)
	case minutes == 0:
		return fmt.Sprintf("%dh", hours)
	default:
		return fmt.Sprintf("%dh %dm", hours, minutes)
	}
}

type summaryField struct {
	Label string
	Value string
}

func packSummaryFields(fields []summaryField, width int) [][]int {
	if width <= 0 {
		group := make([]int, len(fields))
		for i := range fields {
			group[i] = i
		}
		return [][]int{group}
	}
	maxWidth := width - 1
	if maxWidth < 1 {
		maxWidth = 1
	}
	groups := [][]int{}
	current := []int{}
	for index := range fields {
		candidate := append(append([]int(nil), current...), index)
		if len(current) > 0 && codextui.DisplayWidth(summaryLine(fields, candidate)) > maxWidth {
			groups = append(groups, current)
			current = []int{index}
		} else {
			current = candidate
		}
	}
	if len(current) > 0 {
		groups = append(groups, current)
	}
	return groups
}

func summaryLine(fields []summaryField, indexes []int) string {
	var builder strings.Builder
	for i, index := range indexes {
		if i > 0 {
			builder.WriteString(" · ")
		}
		builder.WriteString(fields[index].Label)
		builder.WriteByte(' ')
		builder.WriteString(fields[index].Value)
	}
	return builder.String()
}

func graphWidth(width int) int {
	if width <= 0 {
		return 0
	}
	return tokenActivityChartLeftWidth + shownTokenActivityColumns(width)*2 - 1
}

func TokenActivityChartLines(view TokenActivityView, buckets []TokenActivityDailyBucket, today time.Time, width int) []string {
	values := TokenActivityDailyValues(buckets, today)
	shownColumns := shownTokenActivityColumns(width)
	if shownColumns == 0 {
		return []string{"   Widen terminal to show activity graph"}
	}
	levels := tokenActivityLevelsForView(values, view)
	firstColumn := tokenActivityWeekCount - shownColumns
	lines := []string{tokenActivityMonthLabels(today, firstColumn, shownColumns)}
	for row := 0; row < tokenActivityDayCount; row++ {
		var builder strings.Builder
		builder.WriteString(tokenActivityWeekdayLabel(view, row))
		for column := firstColumn; column < tokenActivityWeekCount; column++ {
			if column > firstColumn {
				builder.WriteByte(' ')
			}
			index := column*tokenActivityDayCount + row
			if view == TokenActivityDaily && tokenActivityCellDate(today, index).After(tokenActivityDay(today)) {
				builder.WriteByte(' ')
				continue
			}
			builder.WriteString(tokenActivityGlyph(view, levels[index]))
		}
		lines = append(lines, builder.String())
	}
	lines = append(lines, "")
	if view == TokenActivityDaily {
		lines = append(lines, tokenActivityLegendLine())
	} else {
		lines = append(lines, tokenActivityBarCaption(view, values))
	}
	lines = append(lines, tokenActivityViewFooter(view))
	return lines
}

func shownTokenActivityColumns(width int) int {
	columns := (width - tokenActivityChartLeftWidth + 1) / 2
	if columns < 0 {
		return 0
	}
	if columns > tokenActivityWeekCount {
		return tokenActivityWeekCount
	}
	return columns
}

func tokenActivityMonthLabels(today time.Time, firstColumn int, shownColumns int) string {
	if shownColumns <= 0 {
		return ""
	}
	cells := make([]rune, shownColumns*2-1)
	for i := range cells {
		cells[i] = ' '
	}
	start := tokenActivityChartStart(today)
	lastEnd := 0
	for column := firstColumn; column < tokenActivityWeekCount; column++ {
		date := start.AddDate(0, 0, column*tokenActivityDayCount)
		if date.Day() > 7 {
			continue
		}
		label := date.Format("Jan")
		offset := (column - firstColumn) * 2
		if offset < lastEnd || offset+len(label) > len(cells) {
			continue
		}
		for index, ch := range label {
			cells[offset+index] = ch
		}
		lastEnd = offset + len(label) + 1
	}
	return "    " + string(cells)
}

func TokenActivityDailyValues(buckets []TokenActivityDailyBucket, today time.Time) []int64 {
	start := tokenActivityChartStart(today)
	end := start.AddDate(0, 0, tokenActivityCellCount)
	byDate := map[time.Time]int64{}
	for _, bucket := range buckets {
		date, err := time.ParseInLocation("2006-01-02", strings.TrimSpace(bucket.StartDate), time.UTC)
		if err != nil {
			continue
		}
		date = tokenActivityDay(date)
		if date.Before(start) || !date.Before(end) || date.After(tokenActivityDay(today)) {
			continue
		}
		tokens := bucket.Tokens
		if tokens < 0 {
			tokens = 0
		}
		byDate[date] += tokens
	}
	values := make([]int64, tokenActivityCellCount)
	for index := range values {
		values[index] = byDate[start.AddDate(0, 0, index)]
	}
	return values
}

func tokenActivityLevelsForView(values []int64, view TokenActivityView) []int {
	switch view {
	case TokenActivityWeekly:
		return tokenActivityBarLevels(tokenActivityWeeklyTotals(values))
	case TokenActivityCumulative:
		weekly := tokenActivityWeeklyTotals(values)
		running := make([]int64, len(weekly))
		var sum int64
		for i, value := range weekly {
			sum += value
			running[i] = sum
		}
		return tokenActivityBarLevels(running)
	default:
		return tokenActivityGradedLevels(values)
	}
}

func tokenActivityGradedLevels(values []int64) []int {
	maxValue := maxInt64(values)
	levels := make([]int, len(values))
	for i, value := range values {
		switch {
		case value == 0 || maxValue == 0:
			levels[i] = 0
		case value*4 > maxValue*3:
			levels[i] = 4
		case value*2 > maxValue:
			levels[i] = 3
		case value*4 > maxValue:
			levels[i] = 2
		default:
			levels[i] = 1
		}
	}
	return levels
}

func tokenActivityWeeklyTotals(values []int64) []int64 {
	totals := make([]int64, 0, tokenActivityWeekCount)
	for start := 0; start < len(values); start += tokenActivityDayCount {
		var sum int64
		end := start + tokenActivityDayCount
		if end > len(values) {
			end = len(values)
		}
		for _, value := range values[start:end] {
			sum += value
		}
		totals = append(totals, sum)
	}
	return totals
}

func tokenActivityBarLevels(totals []int64) []int {
	maxValue := maxInt64(totals)
	levels := make([]int, 0, len(totals)*tokenActivityDayCount)
	for _, value := range totals {
		height := 0
		if value > 0 && maxValue > 0 {
			height = int((value*tokenActivityDayCount + maxValue - 1) / maxValue)
		}
		for row := 0; row < tokenActivityDayCount; row++ {
			if tokenActivityDayCount-row <= height {
				levels = append(levels, 4)
			} else {
				levels = append(levels, 0)
			}
		}
	}
	return levels
}

func tokenActivityWeekdayLabel(view TokenActivityView, row int) string {
	if view != TokenActivityDaily {
		switch row {
		case 0:
			return "max "
		case 6:
			return "  0 "
		default:
			return "    "
		}
	}
	switch row {
	case 0:
		return " Su "
	case 1:
		return " Mo "
	case 2:
		return " Tu "
	case 3:
		return " We "
	case 4:
		return " Th "
	case 5:
		return " Fr "
	case 6:
		return " Sa "
	default:
		return "    "
	}
}

func tokenActivityGlyph(view TokenActivityView, level int) string {
	if view != TokenActivityDaily {
		if level == 0 {
			return " "
		}
		return tokenActivityBarCellGlyph
	}
	if level == 0 {
		return tokenActivityEmptyCellGlyph
	}
	return tokenActivityActiveCellGlyph
}

func tokenActivityLegendLine() string {
	parts := []string{"   Less"}
	for level := 0; level <= 4; level++ {
		parts = append(parts, tokenActivityGlyph(TokenActivityDaily, level))
	}
	return strings.Join(parts, " ") + " More"
}

func tokenActivityBarCaption(view TokenActivityView, values []int64) string {
	weekly := tokenActivityWeeklyTotals(values)
	lead := "Each column = 1 week" + tokenActivitySeparator + "tallest "
	peak := maxInt64(weekly)
	if view == TokenActivityCumulative {
		lead = "Running total" + tokenActivitySeparator + "top "
		peak = sumInt64(weekly)
	}
	if peak <= 0 {
		return "   No token activity in the last 12 months"
	}
	return "   " + lead + FormatTokensCompact(peak)
}

func tokenActivityViewFooter(active TokenActivityView) string {
	names := []struct {
		view TokenActivityView
		name string
	}{
		{TokenActivityDaily, "daily"},
		{TokenActivityWeekly, "weekly"},
		{TokenActivityCumulative, "cumulative"},
	}
	parts := make([]string, 0, len(names))
	for _, item := range names {
		name := item.name
		if item.view == active {
			name = strings.ToUpper(name)
		}
		parts = append(parts, name)
	}
	return "   " + strings.Join(parts, tokenActivitySeparator)
}

func tokenActivityChartStart(today time.Time) time.Time {
	day := tokenActivityDay(today)
	weekStart := day.AddDate(0, 0, -int(day.Weekday()))
	return weekStart.AddDate(0, 0, -(tokenActivityWeekCount-1)*tokenActivityDayCount)
}

func tokenActivityCellDate(today time.Time, index int) time.Time {
	return tokenActivityChartStart(today).AddDate(0, 0, index)
}

func tokenActivityDay(value time.Time) time.Time {
	if value.IsZero() {
		value = time.Now()
	}
	year, month, day := value.Date()
	return time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
}

func maxInt64(values []int64) int64 {
	if len(values) == 0 {
		return 0
	}
	sorted := append([]int64(nil), values...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] > sorted[j] })
	return sorted[0]
}

func sumInt64(values []int64) int64 {
	var sum int64
	for _, value := range values {
		sum += value
	}
	return sum
}

func strconvFormatFloat(value float64, decimals int) string {
	scale := math.Pow10(decimals)
	if decimals >= 0 {
		value = math.Round(value*scale) / scale
	}
	formatted := fmt.Sprintf("%.*f", decimals, value)
	if strings.Contains(formatted, ".") {
		formatted = strings.TrimRight(formatted, "0")
		formatted = strings.TrimRight(formatted, ".")
	}
	return formatted
}
