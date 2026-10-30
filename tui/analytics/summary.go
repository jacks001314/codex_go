package analytics

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"codex_go/auth"
	"codex_go/chatgptapi"
	"codex_go/tui/chatwidget"
)

// LoadAnalyticsProfile reads the full account profile independently of the
// report loads, so a failed report never hides the summary (Rust Live::profile,
// #45769).
func LoadAnalyticsProfile(ctx context.Context, live *Live) (*chatgptapi.AccountProfile, error) {
	if live == nil {
		return nil, nil
	}
	session, err := live.Session(ctx)
	if err != nil {
		return nil, err
	}
	profile, requestErr := RequestAnalytics(ctx, session.Backend, func(ctx context.Context, client *chatgptapi.CloudClient) (*chatgptapi.AccountProfile, error) {
		return client.GetAccountProfile(ctx)
	})
	if err := session.Backend.EnsureIdentity(); err != nil {
		return nil, err
	}
	if requestErr != nil {
		return nil, errors.New(analyticsRequestErrorMessage(requestErr))
	}
	return profile, nil
}

// AnalyticsSummaryViews lists the token activity views offered by the Summary
// tab, in grouping order (Rust analytics::summary::VIEWS, #45769).
var AnalyticsSummaryViews = [3]chatwidget.TokenActivityView{
	chatwidget.TokenActivityDaily,
	chatwidget.TokenActivityWeekly,
	chatwidget.TokenActivityCumulative,
}

// AnalyticsSummaryViewIndex clamps a grouping position onto the view list.
func AnalyticsSummaryViewIndex(group int) int {
	if group < 0 || group >= len(AnalyticsSummaryViews) {
		return 0
	}
	return group
}

// AnalyticsSummaryViewLabel names one Summary grouping.
func AnalyticsSummaryViewLabel(group int) string {
	return AnalyticsSummaryViews[AnalyticsSummaryViewIndex(group)].Label()
}

// AnalyticsSummaryOptions describes the account Summary body. Profile carries
// the decoded payload; Message is the load placeholder or error while Ready is
// false.
type AnalyticsSummaryOptions struct {
	Profile *chatgptapi.AccountProfile
	Message string
	Ready   bool
	Plan    *auth.PlanType
	Zoomed  bool
	View    int
	EndDate time.Time
	Width   int
}

// AnalyticsSummaryLines renders the profile identity, token totals, activity
// chart, insights and most-used plugins and skills (Rust summary_lines).
func AnalyticsSummaryLines(opts AnalyticsSummaryOptions) []string {
	width := opts.Width
	if width < 1 {
		width = 1
	}
	innerWidth := width
	if innerWidth > 110 {
		innerWidth = 110
	}
	if !opts.Ready || opts.Profile == nil {
		message := strings.TrimSpace(opts.Message)
		if message == "" {
			message = "Profile statistics unavailable."
		}
		return analyticsWrap(message, innerWidth)
	}
	profile := opts.Profile
	lines := make([]string, 0, 32)
	if identity := profile.Profile; identity != nil {
		if name := strings.TrimSpace(analyticsStringValue(identity.DisplayName)); name != "" {
			lines = append(lines, analyticsCenter(analyticsTruncate(name, innerWidth), innerWidth))
		}
		if username := strings.TrimSpace(analyticsStringValue(identity.Username)); username != "" {
			handle := "@" + strings.TrimLeft(username, "@")
			lines = append(lines, analyticsCenter(analyticsTruncate(handle, innerWidth), innerWidth))
		}
	}
	if opts.Plan != nil {
		lines = append(lines, analyticsCenter(analyticsPlanLabel(*opts.Plan), innerWidth))
	}
	lines = append(lines, "")
	tokens := profile.Stats.TokenUsageProfileStats
	fields := [][2]string{
		{"Lifetime tokens", analyticsCompactAmountPtr(tokens.LifetimeTokens)},
		{"Peak tokens", analyticsCompactAmountPtr(tokens.PeakDailyTokens)},
		{"Longest chat", analyticsDuration(tokens.LongestRunningTurnSec)},
		{"Current streak", analyticsDays(tokens.CurrentStreakDays)},
		{"Longest streak", analyticsDays(tokens.LongestStreakDays)},
	}
	if !opts.Zoomed {
		for _, field := range fields {
			lines = append(lines, analyticsColumns(field[0], analyticsValueOrDash(field[1]), innerWidth))
		}
		return analyticsSummaryPadded(lines, width, innerWidth)
	}
	count := 1
	switch {
	case innerWidth >= 90:
		count = 5
	case innerWidth >= 54:
		count = 3
	case innerWidth >= 36:
		count = 2
	}
	for start := 0; start < len(fields); start += count {
		end := start + count
		if end > len(fields) {
			end = len(fields)
		}
		chunk := fields[start:end]
		cellWidth := innerWidth / len(chunk)
		values := ""
		labels := ""
		for index, field := range chunk {
			valueCell := analyticsGridCell(analyticsValueOrDash(field[1]), cellWidth)
			labelCell := analyticsGridCell(field[0], cellWidth)
			values += valueCell
			labels += labelCell
			if index+1 < len(chunk) {
				values += strings.Repeat(" ", analyticsMaxInt(cellWidth-analyticsDisplayWidth(valueCell), 0))
				labels += strings.Repeat(" ", analyticsMaxInt(cellWidth-analyticsDisplayWidth(labelCell), 0))
			}
		}
		lines = append(lines, values, labels, "")
	}
	selected := AnalyticsSummaryViewIndex(opts.View)
	// Rust #45770 shows only the active activity view beside the heading.
	controlLine := AnalyticsSummaryViews[selected].Label()
	if innerWidth >= 58 {
		lines = append(lines, analyticsColumns("Token activity", controlLine, innerWidth))
	} else {
		lines = append(lines, "Token activity")
		lines = append(lines, analyticsWrap(controlLine, innerWidth)...)
	}
	lines = append(lines, "Last 12 months", "")
	if tokens.DailyUsageBuckets != nil {
		buckets := make([]chatwidget.TokenActivityDailyBucket, 0, len(*tokens.DailyUsageBuckets))
		for _, bucket := range *tokens.DailyUsageBuckets {
			buckets = append(buckets, chatwidget.TokenActivityDailyBucket{StartDate: bucket.StartDate, Tokens: bucket.Tokens})
		}
		lines = append(lines, chatwidget.TokenActivityCalendarLines(AnalyticsSummaryViews[selected], buckets, opts.EndDate, innerWidth)...)
	} else {
		lines = append(lines, "Token activity history unavailable")
	}
	lines = append(lines, "")
	stats := profile.Stats
	reasoning := "—"
	if effort := strings.TrimSpace(analyticsStringValue(stats.MostUsedReasoningEffort)); effort != "" {
		reasoning = effort + " · " + analyticsPercent(stats.MostUsedReasoningEffortPercentage)
	}
	metrics := [][2]string{
		{"Fast Mode", analyticsPercent(stats.FastModeUsagePercentage)},
		{"Most used reasoning", reasoning},
		{"Skills explored", analyticsCountValue(stats.UniqueSkillsUsed)},
		{"Total skills used", analyticsCountValue(stats.TotalSkillsUsed)},
		{"Total chats", analyticsCountValue(stats.TotalThreads)},
	}
	twoColumns := innerWidth >= 76
	columnWidth := innerWidth
	if twoColumns {
		columnWidth = (innerWidth - 4) / 2
	}
	insights := []string{"Activity insights", ""}
	for _, metric := range metrics {
		insights = append(insights, analyticsWrap(analyticsColumns(metric[0], metric[1], columnWidth), columnWidth)...)
	}
	plugins := []string{"Most used plugins and skills", ""}
	if stats.TopInvocations == nil {
		plugins = append(plugins, "Plugin and skill usage unavailable.")
	} else {
		displayed := 0
		for _, invocation := range *stats.TopInvocations {
			var prefix, name string
			switch invocation.Kind {
			case chatgptapi.ProfileInvocationPlugin:
				prefix, name = "@", analyticsStringValue(invocation.PluginName)
			case chatgptapi.ProfileInvocationSkill:
				prefix, name = "$", analyticsStringValue(invocation.SkillName)
			default:
				continue
			}
			name = strings.TrimSpace(name)
			if name == "" {
				continue
			}
			value := "—"
			if invocation.UsageCount != nil {
				unit := "runs"
				if *invocation.UsageCount == 1 {
					unit = "run"
				}
				value = analyticsCountValue(invocation.UsageCount) + " " + unit
			}
			label := analyticsTruncate(prefix+name, analyticsMaxInt(columnWidth-analyticsDisplayWidth(value)-1, 1))
			plugins = append(plugins, analyticsWrap(analyticsColumns(label, value, columnWidth), columnWidth)...)
			displayed++
			if displayed == 5 {
				break
			}
		}
		if displayed == 0 {
			plugins = append(plugins, "No reported plugins or skills.")
		}
	}
	if twoColumns {
		lines = append(lines, analyticsJoinColumns(insights, plugins, columnWidth)...)
	} else {
		lines = append(lines, insights...)
		lines = append(lines, "")
		lines = append(lines, plugins...)
	}
	if metadata := profile.Metadata; metadata != nil {
		lines = append(lines, "")
		if date, err := time.Parse("2006-01-02", analyticsStringValue(metadata.StatsAsOf)); err == nil {
			lines = append(lines, "Statistics as of "+date.Format("Jan 2, 2006"))
		}
		if strings.TrimSpace(analyticsStringValue(metadata.StatsError)) != "" {
			lines = append(lines, "Some profile statistics are unavailable.")
		}
	}
	return analyticsSummaryPadded(lines, width, innerWidth)
}

// analyticsSummaryPadded wraps the summary to the inner width and indents it
// inside a wider panel (Rust summary_lines padding).
func analyticsSummaryPadded(lines []string, width, innerWidth int) []string {
	wrapped := AnalyticsWrapLines(lines, innerWidth)
	padding := strings.Repeat(" ", (width-innerWidth)/2)
	for index, line := range wrapped {
		if line == "" {
			continue
		}
		wrapped[index] = padding + line
	}
	return wrapped
}

// analyticsGridCell centers value inside one grid cell.
func analyticsGridCell(value string, width int) string {
	if width < 1 {
		width = 1
	}
	cell := analyticsTruncate(value, width)
	return analyticsCenter(cell, width)
}

func analyticsCenter(line string, width int) string {
	padding := width - analyticsDisplayWidth(line)
	if padding < 0 {
		padding = 0
	}
	return strings.Repeat(" ", padding/2) + line
}

func analyticsValueOrDash(value string) string {
	if value == "" {
		return "—"
	}
	return value
}

func analyticsStringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

// analyticsPlanLabel is the Summary's short plan name (Rust summary_lines).
func analyticsPlanLabel(plan auth.PlanType) string {
	switch plan {
	case auth.PlanFree:
		return "Free"
	case auth.PlanGo:
		return "Go"
	case auth.PlanPlus:
		return "Plus"
	case auth.PlanPro:
		return "Pro 200"
	case auth.PlanProlite:
		return "Pro 100"
	case auth.PlanProMax:
		return "Pro 500"
	case auth.PlanTeam, auth.PlanBusiness, auth.PlanSelfServeBusinessProlite, auth.PlanSelfServeBusinessUsageBased:
		return "Business"
	case auth.PlanEnt26, auth.PlanEnterpriseCBPAutomation, auth.PlanEnterpriseCBPUsageBased, auth.PlanEnterprise:
		return "Enterprise"
	case auth.PlanEdu, auth.PlanEduPlus, auth.PlanEduPro:
		return "Education"
	default:
		return "Account"
	}
}

// analyticsCompactAmount mirrors Rust data::compact_amount: one decimal with
// trailing zeros removed, promoting before rounding would print 1000K.
func analyticsCompactAmount(value float64) string {
	if math.Abs(value) < 1000 || math.IsNaN(value) || math.IsInf(value, 0) {
		return analyticsGroupedAmount(value)
	}
	units := []string{"K", "M", "B", "T"}
	scaled := value / 1000
	unit := 0
	for math.Abs(scaled) >= 999.95 && unit+1 < len(units) {
		scaled /= 1000
		unit++
	}
	rounded := strconv.FormatFloat(math.Round(scaled*10)/10, 'f', 1, 64)
	rounded = strings.TrimRight(strings.TrimRight(rounded, "0"), ".")
	return rounded + units[unit]
}

func analyticsCompactAmountPtr(value *int64) string {
	if value == nil {
		return ""
	}
	return analyticsCompactAmount(float64(*value))
}

// analyticsGroupedAmount formats a whole value with thousands separators.
func analyticsGroupedAmount(value float64) string {
	rounded := int64(math.Round(value))
	digits := strconv.FormatInt(rounded, 10)
	sign := ""
	if strings.HasPrefix(digits, "-") {
		sign, digits = "-", digits[1:]
	}
	var builder strings.Builder
	for index, char := range digits {
		if index > 0 && (len(digits)-index)%3 == 0 {
			builder.WriteByte(',')
		}
		builder.WriteRune(char)
	}
	return sign + builder.String()
}

// analyticsPercent renders a bounded backend percentage (Rust percent).
func analyticsPercent(value *json.Number) string {
	if value == nil {
		return "—"
	}
	parsed, err := value.Float64()
	if err != nil || math.IsNaN(parsed) || math.IsInf(parsed, 0) || parsed < 0 || parsed > 100 {
		return "—"
	}
	return strconv.FormatFloat(math.Round(parsed), 'f', 0, 64) + "%"
}

// analyticsCountValue groups a count and keeps a missing count distinct.
func analyticsCountValue(value *int64) string {
	if value == nil {
		return "—"
	}
	return analyticsGroupedAmount(float64(*value))
}

// analyticsDays renders a streak length (Rust summary_lines).
func analyticsDays(value *int64) string {
	if value == nil {
		return ""
	}
	return strconv.FormatInt(*value, 10) + " days"
}

// analyticsDuration renders a turn length as seconds, minutes or hours.
func analyticsDuration(value *int64) string {
	if value == nil {
		return ""
	}
	seconds := *value
	if seconds < 0 {
		seconds = 0
	}
	hours, minutes := seconds/3600, seconds%3600/60
	switch {
	case hours == 0 && minutes == 0:
		return fmt.Sprintf("%ds", seconds)
	case hours == 0:
		return fmt.Sprintf("%dm", minutes)
	case minutes == 0:
		return fmt.Sprintf("%dh", hours)
	default:
		return fmt.Sprintf("%dh %dm", hours, minutes)
	}
}
