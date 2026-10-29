package analytics

import (
	"errors"
	"math"
	"sort"
	"strings"
	"time"

	"codex_go/chatgptapi"
)

// historyForResponse projects one typed backend report and normalizes it.
func historyForResponse(
	response chatgptapi.AnalyticsResponse,
	report AccountAnalyticsReport,
	grouping AccountAnalyticsGrouping,
	start time.Time,
	end time.Time,
) (*AccountAnalyticsHistory, error) {
	return history(projectAnalyticsResponse(response), report, grouping, start, end)
}

// history converts one projected report into display data while preserving
// missing values and full denominators (Rust normalize.rs, #45740). A nil
// history without an error is Rust's Ok(None): the report kind is valid but this
// response carries no usable breakdown.
func history(
	data analyticsData,
	report AccountAnalyticsReport,
	grouping AccountAnalyticsGrouping,
	start time.Time,
	end time.Time,
) (*AccountAnalyticsHistory, error) {
	if report == AccountAnalyticsReportMessages {
		switch grouping {
		case AccountAnalyticsGroupingModel, AccountAnalyticsGroupingSurface:
		default:
			return nil, errors.New("Unsupported message count grouping.")
		}
	}
	days := map[time.Time]*analyticsDayAccumulator{}
	for _, record := range data.Data {
		date, err := analyticsRecordDate(record.Date)
		if err != nil {
			return nil, err
		}
		if date.Before(start) || date.After(end) {
			continue
		}
		accumulator := days[date]
		if accumulator == nil {
			accumulator = &analyticsDayAccumulator{values: map[string]float64{}}
			days[date] = accumulator
		}
		if err := accumulateAnalyticsRecord(record, report, grouping, accumulator); err != nil {
			if errors.Is(err, errAnalyticsUnavailable) {
				return nil, nil
			}
			return nil, err
		}
		if !isFinite(accumulator.total) {
			return nil, errors.New("Analytics returned an invalid amount.")
		}
		for _, value := range accumulator.values {
			if !isFinite(value) || (report != AccountAnalyticsReportCredits && value < 0) {
				return nil, errors.New("Analytics returned an invalid amount.")
			}
		}
	}
	if report == AccountAnalyticsReportUsage && grouping == AccountAnalyticsGroupingModel && unitsOf(data) != "credits" {
		foldSmallModelUsage(days)
	}
	if report == AccountAnalyticsReportCredits {
		for date := start; !date.After(end); date = date.AddDate(0, 0, 1) {
			if days[date] == nil {
				days[date] = &analyticsDayAccumulator{values: map[string]float64{}}
			}
		}
	}
	return &AccountAnalyticsHistory{
		Unit:      analyticsHistoryUnit(report, data),
		UpdatedAt: analyticsFreshness(data.DataFreshnessTS),
		Data:      analyticsHistoryDays(days, data.Series),
	}, nil
}

type analyticsDayAccumulator struct {
	total  float64
	values map[string]float64
}

// accumulateAnalyticsRecord applies one report record to its day and reports
// Ok(None) as a sentinel error so the caller stops without surfacing a failure.
func accumulateAnalyticsRecord(record analyticsRecord, report AccountAnalyticsReport, grouping AccountAnalyticsGrouping, accumulator *analyticsDayAccumulator) error {
	switch report {
	case AccountAnalyticsReportUsage:
		return accumulateUsageRecord(record, grouping, accumulator)
	case AccountAnalyticsReportCredits:
		return accumulateCreditsRecord(record, grouping, accumulator)
	case AccountAnalyticsReportMessages:
		return accumulateMessagesRecord(record, grouping, accumulator)
	case AccountAnalyticsReportPlugins, AccountAnalyticsReportSkills:
		return accumulateToolRecord(record, report, accumulator)
	default:
		return nil
	}
}

func accumulateUsageRecord(record analyticsRecord, grouping AccountAnalyticsGrouping, accumulator *analyticsDayAccumulator) error {
	var daily map[string]float64
	dailyReported := false
	switch grouping {
	case AccountAnalyticsGroupingSurface:
		if record.ProductSurfaceUsageValues != nil {
			daily = record.ProductSurfaceUsageValues
			dailyReported = true
		}
	case AccountAnalyticsGroupingModel:
		if record.Models != nil {
			daily = map[string]float64{}
			for _, model := range record.Models {
				if model.Credits == nil {
					return errors.New("Plan usage amount was not reported.")
				}
				daily[model.Model] += *model.Credits
			}
			dailyReported = true
		}
	}
	if dailyReported {
		for key, amount := range daily {
			if !isFinite(amount) || amount < 0 {
				return errors.New("Analytics returned an invalid amount.")
			}
			normalized := key
			switch {
			case grouping == AccountAnalyticsGroupingSurface && strings.HasPrefix(key, "work_"):
				normalized = "work"
			case grouping == AccountAnalyticsGroupingSurface:
				normalized = "codex"
			case strings.EqualFold(key, "other"):
				normalized = "other"
			}
			accumulator.total += amount
			accumulator.values[normalized] += amount
		}
		return nil
	}
	if record.Attribution == nil {
		return errAnalyticsUnavailable
	}
	for _, entry := range record.Attribution {
		if !isFinite(entry.Value) || entry.Value < 0 {
			return errors.New("Analytics returned an invalid amount.")
		}
		accumulator.total += entry.Value
		if grouping == AccountAnalyticsGroupingTaskStart && pointerValue(entry.ThreadSource) != "user" {
			continue
		}
		var key string
		switch grouping {
		case AccountAnalyticsGroupingFeature:
			key = pointerValueOr(entry.ThreadSource, "unknown")
		case AccountAnalyticsGroupingModel:
			key = pointerValueOr(entry.Model, "unknown")
		case AccountAnalyticsGroupingSurface:
			key = pointerValueOr(entry.Surface, "unknown")
		case AccountAnalyticsGroupingTaskStart:
			if entry.TurnTrigger != nil {
				key = "start-" + *entry.TurnTrigger
			} else {
				key = "unknown"
			}
		default:
			return errors.New("unsupported analytics grouping")
		}
		accumulator.values[key] += entry.Value
	}
	return nil
}

func accumulateCreditsRecord(record analyticsRecord, grouping AccountAnalyticsGrouping, accumulator *analyticsDayAccumulator) error {
	switch {
	case record.Values != nil:
		for key, amount := range record.Values {
			accumulator.values[key] += amount
			accumulator.total += amount
		}
	case grouping == AccountAnalyticsGroupingModel || grouping == AccountAnalyticsGroupingSpeed:
		if record.Models == nil {
			return errAnalyticsUnavailable
		}
		for _, model := range record.Models {
			amount := 0.0
			if model.OnDemandCredits != nil {
				amount = *model.OnDemandCredits
			}
			key := model.Model
			if grouping == AccountAnalyticsGroupingSpeed {
				key = pointerValueOr(model.Speed, "unknown")
			}
			accumulator.values[key] += amount
			accumulator.total += amount
		}
	case record.PremiumUsageValues != nil:
		for surface, amount := range record.PremiumUsageValues.CreditUsageCredits {
			if !isFinite(amount) {
				return errors.New("Analytics returned an invalid amount.")
			}
			accumulator.values[surface] += amount
			accumulator.total += amount
		}
	case record.CreditAmount != nil && record.ProductSurface != nil:
		accumulator.values[*record.ProductSurface] += *record.CreditAmount
		accumulator.total += *record.CreditAmount
	default:
		return errAnalyticsUnavailable
	}
	return nil
}

func accumulateMessagesRecord(record analyticsRecord, grouping AccountAnalyticsGrouping, accumulator *analyticsDayAccumulator) error {
	if record.Totals == nil {
		return errors.New("Message count is unavailable.")
	}
	authoritative := record.Totals.Turns
	if !isFinite(authoritative) || authoritative < 0 {
		return errors.New("Analytics returned an invalid amount.")
	}
	recordValues := map[string]float64{}
	if grouping == AccountAnalyticsGroupingModel {
		for _, model := range record.Models {
			key := model.Model
			if strings.EqualFold(key, "codex-auto-review") || strings.EqualFold(key, "other") {
				key = "other"
			}
			if model.Turns == nil {
				return errors.New("Turn count is unavailable.")
			}
			count := *model.Turns
			if !isFinite(count) || count < 0 {
				return errors.New("Analytics returned an invalid amount.")
			}
			recordValues[key] += count
		}
	} else {
		for _, client := range record.Clients {
			if !isFinite(client.Turns) || client.Turns < 0 {
				return errors.New("Analytics returned an invalid amount.")
			}
			recordValues[messageClientKey(client.ClientID)] += client.Turns
		}
	}
	total := 0.0
	for _, count := range recordValues {
		total += count
	}
	if remainder := math.Max(authoritative-total, 0); remainder > 0 {
		recordValues["other"] += remainder
	}
	for key, count := range recordValues {
		accumulator.values[key] += count
	}
	accumulator.total += authoritative
	return nil
}

func accumulateToolRecord(record analyticsRecord, report AccountAnalyticsReport, accumulator *analyticsDayAccumulator) error {
	overviews := record.PluginUsageOverviews
	if report == AccountAnalyticsReportSkills {
		overviews = record.SkillUsageOverviews
	}
	if overviews == nil {
		return errors.New("Tool activity is unavailable.")
	}
	for _, tool := range overviews {
		if !isFinite(tool.InvocationCounts) || tool.InvocationCounts < 0 {
			return errors.New("Analytics returned an invalid amount.")
		}
		accumulator.values[tool.DisplayName] += tool.InvocationCounts
		accumulator.total += tool.InvocationCounts
	}
	return nil
}

// foldSmallModelUsage groups models that contribute less than 1% of the period
// total into "other" for relative-usage reports (Rust normalize.rs).
func foldSmallModelUsage(days map[time.Time]*analyticsDayAccumulator) {
	totals := map[string]float64{}
	for _, accumulator := range days {
		for key, value := range accumulator.values {
			totals[key] += value
		}
	}
	keys := sortedFloatKeys(totals)
	grandTotal := 0.0
	for _, key := range keys {
		grandTotal += totals[key]
	}
	for _, accumulator := range days {
		other := 0.0
		for key, value := range accumulator.values {
			amount := totals[key]
			if strings.EqualFold(key, "other") || (amount > 0 && amount < grandTotal*0.01) {
				other += value
				delete(accumulator.values, key)
			}
		}
		if other > 0 {
			accumulator.values["other"] = other
		}
	}
}

func analyticsHistoryUnit(report AccountAnalyticsReport, data analyticsData) AccountAnalyticsUnit {
	switch report {
	case AccountAnalyticsReportUsage:
		if unitsOf(data) == "credits" {
			return AccountAnalyticsUnitCredits
		}
		return AccountAnalyticsUnitRelativeUsage
	case AccountAnalyticsReportCredits:
		return AccountAnalyticsUnitCredits
	default:
		return AccountAnalyticsUnitCount
	}
}

func analyticsHistoryDays(days map[time.Time]*analyticsDayAccumulator, series []analyticsSeries) []AccountAnalyticsDay {
	dates := make([]time.Time, 0, len(days))
	for date := range days {
		dates = append(dates, date)
	}
	sort.Slice(dates, func(i, j int) bool { return dates[i].Before(dates[j]) })
	labels := map[string]string{}
	for _, entry := range series {
		labels[entry.Key] = entry.Label
	}
	history := make([]AccountAnalyticsDay, 0, len(dates))
	for _, date := range dates {
		accumulator := days[date]
		day := AccountAnalyticsDay{Date: date, Total: accumulator.total}
		for _, key := range sortedFloatKeys(accumulator.values) {
			label, ok := labels[key]
			if !ok {
				label = AccountAnalyticsLabel(key)
			}
			day.Values = append(day.Values, AccountAnalyticsValue{
				Key:   key,
				Label: label,
				Value: accumulator.values[key],
			})
		}
		history = append(history, day)
	}
	return history
}

func analyticsRecordDate(value string) (time.Time, error) {
	trimmed := value
	if len(trimmed) > 10 {
		trimmed = trimmed[:10]
	}
	date, err := time.Parse("2006-01-02", trimmed)
	if err != nil {
		return time.Time{}, errors.New("Analytics returned an invalid date.")
	}
	return date, nil
}

func analyticsFreshness(value *string) *int64 {
	if value == nil {
		return nil
	}
	parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(*value))
	if err != nil {
		return nil
	}
	seconds := parsed.Unix()
	return &seconds
}

func unitsOf(data analyticsData) string {
	if data.Units == nil {
		return ""
	}
	return *data.Units
}

func messageClientKey(clientID string) string {
	switch clientID {
	case "CODEX_CLI":
		return "cli"
	case "CODEX_IDE_VSCODE":
		return "vscode"
	case "CODEX_WEB":
		return "web"
	case "CODEX_DESKTOP_APP", "CODEX_WORK_DESKTOP":
		return "desktop_app"
	case "CODEX_WORK_WEB":
		return "work_web"
	case "CODEX_WORK_MOBILE":
		return "mobile"
	case "CODEX_GITHUB":
		return "github_code_review"
	default:
		return "other"
	}
}

// AccountAnalyticsLabel is Rust normalize::label: short display names for the
// keys the analytics reports use.
func AccountAnalyticsLabel(key string) string {
	switch key {
	case "user":
		return "Tasks"
	case "subagent":
		return "Subagents"
	case "image_generation":
		return "Image generation"
	case "automation":
		return "Automations"
	case "guardian_review":
		return "Guardian review"
	case "automated_review":
		return "Auto review"
	case "agent_identity":
		return "Workspace agents"
	case "memory_consolidation":
		return "Memory consolidation"
	case "cli":
		return "CLI"
	case "desktop_app":
		return "Desktop app"
	case "vscode":
		return "VS Code"
	case "web":
		return "Web"
	case "work_web":
		return "Work web"
	case "work_desktop":
		return "Work desktop"
	case "work_mobile":
		return "Work mobile"
	case "mobile":
		return "Mobile"
	case "slack":
		return "Slack"
	case "linear":
		return "Linear"
	case "jetbrains":
		return "JetBrains"
	case "sdk":
		return "SDK"
	case "exec":
		return "Exec"
	case "github":
		return "GitHub"
	case "codex":
		return "Codex"
	case "work":
		return "Work"
	case "code_review", "github_code_review":
		return "Code review"
	case "start-user", "start-composer":
		return "User messages"
	case "start-goal":
		return "Goals"
	case "start-composer_queue":
		return "Queued messages"
	case "start-composer_queue_run_now":
		return "Queued messages run now"
	case "start-automation_cron_scheduled":
		return "Scheduled automations"
	case "start-automation_cron_run_now":
		return "Automations run now"
	case "start-automation_heartbeat_scheduled":
		return "Scheduled follow-ups"
	case "start-automation_heartbeat_run_now":
		return "Follow-ups run now"
	case "start-app_tool_create_thread":
		return "Agent-created tasks"
	case "start-app_tool_send_message":
		return "Agent follow-ups"
	case "unknown", "start-unknown":
		return "Unknown"
	case "fast":
		return "Fast"
	case "standard":
		return "Standard"
	case "other", "start-other":
		return "Other"
	default:
		return key
	}
}

// errAnalyticsUnavailable is Rust's Ok(None): the report has no usable
// breakdown for this response, which is not an error.
var errAnalyticsUnavailable = errors.New("analytics breakdown unavailable")

func isFinite(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}

func pointerValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func pointerValueOr(value *string, fallback string) string {
	if value == nil {
		return fallback
	}
	return *value
}

func sortedFloatKeys(values map[string]float64) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
