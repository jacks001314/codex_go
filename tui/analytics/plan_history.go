// Package analytics holds the TUI's account-usage report models. Plan history
// mirrors Rust's tui/src/analytics/plan.rs (#45766): period IDs are
// snapshot-scoped, unknown accounting stays unknown, and only the five-hour and
// weekly windows are accepted.
package analytics

import (
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"codex_go/chatgptapi"
)

const (
	maxPlanHistoryPeriods = 1000
	fiveHourWindowMinutes = 300
	weeklyWindowMinutes   = 10080
)

// Plan window buckets, in report order.
const (
	PlanWindowFiveHour = 0
	PlanWindowWeekly   = 1
)

// Period is one parsed allowance window.
type Period struct {
	ID         string
	Start      time.Time
	End        time.Time
	Complete   bool
	Used       *float64
	Breakdowns []chatgptapi.PlanLimitBreakdown
}

// Report is a validated plan history snapshot.
type Report struct {
	AsOf             time.Time
	CoverageStart    *time.Time
	CoverageComplete bool
	Approximate      bool
	Periods          [2][]Period
}

// ParsePlanHistory validates a server snapshot. A snapshot without
// data_as_of has no report, matching Rust.
func ParsePlanHistory(history *chatgptapi.PlanLimitHistory) (*Report, error) {
	if history == nil || history.DataAsOf == nil {
		return nil, nil
	}
	asOf, err := parsePlanTimestamp(*history.DataAsOf)
	if err != nil {
		return nil, err
	}
	var coverageStart *time.Time
	if history.CoverageStart != nil {
		start, err := parsePlanTimestamp(*history.CoverageStart)
		if err != nil {
			return nil, err
		}
		coverageStart = &start
	}
	if len(history.Periods) > maxPlanHistoryPeriods {
		return nil, errors.New("Plan history contains too many periods.")
	}
	report := &Report{
		AsOf:             asOf,
		CoverageStart:    coverageStart,
		CoverageComplete: history.CoverageComplete,
		Approximate:      history.ApproximateOrDefault(),
	}
	seen := make(map[string]bool, len(history.Periods))
	for _, period := range history.Periods {
		window, ok := planWindowIndex(period.WindowMinutes)
		if !ok {
			return nil, errors.New("Unsupported plan history window.")
		}
		start, err := parsePlanTimestamp(period.StartsAt)
		if err != nil {
			return nil, err
		}
		end, err := parsePlanTimestamp(period.EndsAt)
		if err != nil {
			return nil, err
		}
		if !start.Before(end) || seen[period.ID] || !finitePtr(period.UsedBasisPoints) ||
			!finiteBreakdowns(period.Breakdowns) {
			return nil, errors.New("Invalid plan history period.")
		}
		seen[period.ID] = true
		report.Periods[window] = append(report.Periods[window], Period{
			ID:         period.ID,
			Start:      start,
			End:        end,
			Complete:   period.AccountingComplete,
			Used:       period.UsedBasisPoints,
			Breakdowns: period.Breakdowns,
		})
	}
	return report, nil
}

func parsePlanTimestamp(value string) (time.Time, error) {
	parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(value))
	if err != nil {
		return time.Time{}, errors.New("Invalid plan history timestamp.")
	}
	return parsed.UTC(), nil
}

func planWindowIndex(minutes uint32) (int, bool) {
	switch minutes {
	case fiveHourWindowMinutes:
		return PlanWindowFiveHour, true
	case weeklyWindowMinutes:
		return PlanWindowWeekly, true
	default:
		return 0, false
	}
}

func finitePtr(value *float64) bool {
	return value == nil || !math.IsNaN(*value) && !math.IsInf(*value, 0)
}

func finiteBreakdowns(groups []chatgptapi.PlanLimitBreakdown) bool {
	for _, group := range groups {
		for _, row := range group.Rows {
			if math.IsNaN(row.BasisPoints) || math.IsInf(row.BasisPoints, 0) {
				return false
			}
		}
	}
	return true
}

// WindowLabel names one bucket the way the panel header does.
func WindowLabel(window int) string {
	if window == PlanWindowWeekly {
		return "weekly"
	}
	return "5-hour"
}

// PercentLabel renders hundredths of a percent. Unknown usage stays unknown
// rather than becoming zero.
func PercentLabel(basisPoints *float64) string {
	if basisPoints == nil {
		return "unknown"
	}
	percent := *basisPoints / 100
	return strconv.FormatFloat(percent, 'f', 1, 64) + "%"
}

// DimensionLabel names one breakdown dimension.
func DimensionLabel(dimension chatgptapi.PlanLimitDimension) string {
	switch dimension {
	case chatgptapi.PlanLimitDimensionThreadSource:
		return "thread source"
	case chatgptapi.PlanLimitDimensionTurnTrigger:
		return "turn start"
	case chatgptapi.PlanLimitDimensionModel:
		return "model"
	case chatgptapi.PlanLimitDimensionSurface:
		return "surface"
	default:
		return "other"
	}
}

// RenderPlanHistory renders a report as transcript lines. Narrow widths
// truncate each line rather than dropping rows.
func RenderPlanHistory(report *Report, width int) []string {
	if report == nil {
		return []string{"Plan history unavailable for this account."}
	}
	if width <= 0 {
		width = 80
	}
	freshness := "snapshot as of " + report.AsOf.Format(time.RFC3339)
	if report.Approximate {
		freshness += " (approximate)"
	}
	coverage := "coverage complete"
	if !report.CoverageComplete {
		if report.CoverageStart != nil {
			coverage = "coverage from " + report.CoverageStart.Format(time.RFC3339)
		} else {
			coverage = "coverage incomplete"
		}
	}
	lines := []string{"Plan usage history", freshness + " \u00b7 " + coverage}
	for window := range report.Periods {
		periods := report.Periods[window]
		lines = append(lines, WindowLabel(window)+" periods")
		if len(periods) == 0 {
			lines = append(lines, "  no periods reported")
			continue
		}
		for _, period := range periods {
			lines = append(lines, renderPlanPeriod(period))
			lines = append(lines, renderPlanBreakdowns(period)...)
		}
	}
	for i, line := range lines {
		lines[i] = truncateLine(line, width)
	}
	return lines
}

func renderPlanPeriod(period Period) string {
	line := fmt.Sprintf("  %s \u2192 %s  %s",
		period.Start.Format("2006-01-02 15:04"),
		period.End.Format("2006-01-02 15:04"),
		PercentLabel(period.Used))
	if !period.Complete {
		line += " (incomplete accounting)"
	}
	return line
}

func renderPlanBreakdowns(period Period) []string {
	if len(period.Breakdowns) == 0 {
		return nil
	}
	var lines []string
	for _, group := range period.Breakdowns {
		rows := make([]string, 0, len(group.Rows))
		sorted := append([]chatgptapi.PlanLimitValue(nil), group.Rows...)
		sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Key < sorted[j].Key })
		for _, row := range sorted {
			rows = append(rows, row.Key+" "+PercentLabel(&row.BasisPoints))
		}
		lines = append(lines, "    "+DimensionLabel(group.Dimension)+": "+strings.Join(rows, ", "))
	}
	return lines
}

func truncateLine(line string, width int) string {
	runes := []rune(line)
	if len(runes) <= width {
		return line
	}
	if width <= 3 {
		return string(runes[:width])
	}
	return string(runes[:width-3]) + "..."
}
