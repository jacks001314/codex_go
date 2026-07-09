package status

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

const (
	StatusLimitBarSegments      = 20
	RateLimitStaleThresholdMins = 15
	statusLimitBarFilled        = "\u25a0"
	statusLimitBarEmpty         = "\u25a1"
	primaryLimitFallbackLabel   = "usage"
	secondaryLimitFallbackLabel = "secondary usage"
)

type RateLimitStatus struct {
	UsedPercent float64
	Label       string
}

type StatusRateLimitValueKind string

const (
	StatusRateLimitValueWindow StatusRateLimitValueKind = "window"
	StatusRateLimitValueText   StatusRateLimitValueKind = "text"
)

type StatusRateLimitRow struct {
	Label string
	Value StatusRateLimitValue
}

type StatusRateLimitValue struct {
	Kind        StatusRateLimitValueKind
	PercentUsed float64
	ResetsAt    *string
	Details     *string
	Text        string
}

type StatusRateLimitDataKind string

const (
	StatusRateLimitDataAvailable   StatusRateLimitDataKind = "available"
	StatusRateLimitDataStale       StatusRateLimitDataKind = "stale"
	StatusRateLimitDataUnavailable StatusRateLimitDataKind = "unavailable"
	StatusRateLimitDataMissing     StatusRateLimitDataKind = "missing"
)

type StatusRateLimitData struct {
	Kind StatusRateLimitDataKind
	Rows []StatusRateLimitRow
}

type RateLimitWindowDisplay struct {
	UsedPercent   float64
	ResetsAt      *string
	WindowMinutes *int64
}

type RateLimitSnapshotDisplay struct {
	LimitName       string
	CapturedAt      time.Time
	Primary         *RateLimitWindowDisplay
	Secondary       *RateLimitWindowDisplay
	Credits         *CreditsSnapshotDisplay
	IndividualLimit *SpendControlLimitSnapshotDisplay
}

type CreditsSnapshotDisplay struct {
	HasCredits bool
	Unlimited  bool
	Balance    *string
}

type SpendControlLimitSnapshotDisplay struct {
	CapturedAt       time.Time
	PercentRemaining float64
	Used             string
	Limit            string
	ResetsAt         *string
}

func ComposeRateLimitData(snapshot *RateLimitSnapshotDisplay, now time.Time) StatusRateLimitData {
	if snapshot == nil {
		return StatusRateLimitData{Kind: StatusRateLimitDataMissing}
	}
	return ComposeRateLimitDataMany([]RateLimitSnapshotDisplay{*snapshot}, now)
}

func ComposeRateLimitDataMany(snapshots []RateLimitSnapshotDisplay, now time.Time) StatusRateLimitData {
	if len(snapshots) == 0 {
		return StatusRateLimitData{Kind: StatusRateLimitDataMissing}
	}

	rows := make([]StatusRateLimitRow, 0, len(snapshots)*3)
	stale := false

	for _, snapshot := range snapshots {
		if now.Sub(snapshot.CapturedAt) > RateLimitStaleThresholdMins*time.Minute {
			stale = true
		}
		if snapshot.IndividualLimit != nil && now.Sub(snapshot.IndividualLimit.CapturedAt) > RateLimitStaleThresholdMins*time.Minute {
			stale = true
		}

		limitBucketLabel := snapshot.LimitName
		if limitBucketLabel == "" {
			limitBucketLabel = "codex"
		}
		showLimitPrefix := !strings.EqualFold(limitBucketLabel, "codex")
		windowCount := 0
		if snapshot.Primary != nil {
			windowCount++
		}
		if snapshot.Secondary != nil {
			windowCount++
		}
		combineNonCodexSingleLimit := showLimitPrefix && windowCount == 1

		if showLimitPrefix && !combineNonCodexSingleLimit {
			rows = append(rows, StatusRateLimitRow{
				Label: limitBucketLabel + " limit",
				Value: StatusRateLimitValue{Kind: StatusRateLimitValueText},
			})
		}

		if snapshot.Primary != nil {
			label := capitalizeFirst(LimitLabelForWindow(snapshot.Primary.WindowMinutes, false)) + " limit"
			if combineNonCodexSingleLimit {
				label = limitBucketLabel + " " + label
			}
			rows = append(rows, rateLimitWindowRow(label, snapshot.Primary))
		}
		if snapshot.Secondary != nil {
			label := capitalizeFirst(LimitLabelForWindow(snapshot.Secondary.WindowMinutes, true)) + " limit"
			if combineNonCodexSingleLimit {
				label = limitBucketLabel + " " + label
			}
			rows = append(rows, rateLimitWindowRow(label, snapshot.Secondary))
		}
		if snapshot.Credits != nil {
			if row, ok := creditStatusRow(snapshot.Credits); ok {
				rows = append(rows, row)
			}
		}
		if snapshot.IndividualLimit != nil {
			details := snapshot.IndividualLimit.Used + " of " + snapshot.IndividualLimit.Limit + " credits used"
			rows = append(rows, StatusRateLimitRow{
				Label: "Monthly credit limit",
				Value: StatusRateLimitValue{
					Kind:        StatusRateLimitValueWindow,
					PercentUsed: 100.0 - snapshot.IndividualLimit.PercentRemaining,
					ResetsAt:    snapshot.IndividualLimit.ResetsAt,
					Details:     &details,
				},
			})
		}
	}

	if len(rows) == 0 {
		return StatusRateLimitData{Kind: StatusRateLimitDataUnavailable}
	}
	if stale {
		return StatusRateLimitData{Kind: StatusRateLimitDataStale, Rows: rows}
	}
	return StatusRateLimitData{Kind: StatusRateLimitDataAvailable, Rows: rows}
}

func RenderStatusLimitProgressBar(percentRemaining float64) string {
	ratio := clampFloat(percentRemaining/100.0, 0, 1)
	filled := int(ratio*StatusLimitBarSegments + 0.5)
	if filled > StatusLimitBarSegments {
		filled = StatusLimitBarSegments
	}
	empty := StatusLimitBarSegments - filled
	return "[" + strings.Repeat(statusLimitBarFilled, filled) + strings.Repeat(statusLimitBarEmpty, empty) + "]"
}

func FormatStatusLimitSummary(percentRemaining float64) string {
	return fmt.Sprintf("%.0f%% left", percentRemaining)
}

func LimitLabelForWindow(windowMinutes *int64, secondary bool) string {
	if windowMinutes != nil {
		if label, ok := LimitsDuration(*windowMinutes); ok {
			return label
		}
	}
	if secondary {
		return secondaryLimitFallbackLabel
	}
	return primaryLimitFallbackLabel
}

func LimitsDuration(minutes int64) (string, bool) {
	const minutesPerHour int64 = 60
	const minutesPer5Hours = 5 * minutesPerHour
	const minutesPerDay = 24 * minutesPerHour
	const minutesPerWeek = 7 * minutesPerDay
	const minutesPerMonth = 30 * minutesPerDay
	const minutesPerYear = 365 * minutesPerDay

	if minutes < 0 {
		minutes = 0
	}
	switch {
	case isApproximateWindow(minutes, minutesPer5Hours):
		return "5h", true
	case isApproximateWindow(minutes, minutesPerDay):
		return "daily", true
	case isApproximateWindow(minutes, minutesPerWeek):
		return "weekly", true
	case isApproximateWindow(minutes, minutesPerMonth):
		return "monthly", true
	case isApproximateWindow(minutes, minutesPerYear):
		return "annual", true
	default:
		return "", false
	}
}

func rateLimitWindowRow(label string, window *RateLimitWindowDisplay) StatusRateLimitRow {
	return StatusRateLimitRow{
		Label: label,
		Value: StatusRateLimitValue{
			Kind:        StatusRateLimitValueWindow,
			PercentUsed: window.UsedPercent,
			ResetsAt:    window.ResetsAt,
		},
	}
}

func creditStatusRow(credits *CreditsSnapshotDisplay) (StatusRateLimitRow, bool) {
	if credits == nil || !credits.HasCredits {
		return StatusRateLimitRow{}, false
	}
	if credits.Unlimited {
		return StatusRateLimitRow{
			Label: "Credits",
			Value: StatusRateLimitValue{Kind: StatusRateLimitValueText, Text: "Unlimited"},
		}, true
	}
	if credits.Balance == nil {
		return StatusRateLimitRow{}, false
	}
	displayBalance, ok := FormatCreditBalance(*credits.Balance)
	if !ok {
		return StatusRateLimitRow{}, false
	}
	return StatusRateLimitRow{
		Label: "Credits",
		Value: StatusRateLimitValue{Kind: StatusRateLimitValueText, Text: displayBalance + " credits"},
	}, true
}

func FormatCreditBalance(raw string) (string, bool) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", false
	}
	if intValue, err := strconv.ParseInt(trimmed, 10, 64); err == nil && intValue > 0 {
		return strconv.FormatInt(intValue, 10), true
	}
	if value, err := strconv.ParseFloat(trimmed, 64); err == nil && value > 0 {
		return strconv.FormatInt(roundToInt64(value), 10), true
	}
	return "", false
}

func FormatCreditAmount(raw string) (string, bool) {
	value, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
	if err != nil || value < 0 {
		return "", false
	}
	return formatWithSeparators(roundToInt64(value)), true
}

func isApproximateWindow(minutes int64, expected int64) bool {
	value := float64(minutes)
	target := float64(expected)
	return value >= target*0.95 && value <= target*1.05
}

func capitalizeFirst(text string) string {
	if text == "" {
		return ""
	}
	runes := []rune(text)
	if runes[0] >= 'a' && runes[0] <= 'z' {
		runes[0] -= 'a' - 'A'
	}
	return string(runes)
}

func clampFloat(value float64, minValue float64, maxValue float64) float64 {
	if value < minValue {
		return minValue
	}
	if value > maxValue {
		return maxValue
	}
	return value
}

func formatWithSeparators(value int64) string {
	text := strconv.FormatInt(value, 10)
	if len(text) <= 3 {
		return text
	}
	var out strings.Builder
	prefix := len(text) % 3
	if prefix == 0 {
		prefix = 3
	}
	out.WriteString(text[:prefix])
	for i := prefix; i < len(text); i += 3 {
		out.WriteByte(',')
		out.WriteString(text[i : i+3])
	}
	return out.String()
}
