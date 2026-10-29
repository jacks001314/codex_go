package analytics

import (
	"reflect"
	"testing"
	"time"

	"codex_go/chatgptapi"
)

func analyticsDayForTest(t *testing.T, value string) time.Time {
	t.Helper()
	date, err := time.Parse("2006-01-02", value)
	if err != nil {
		t.Fatalf("parse %q: %v", value, err)
	}
	return date
}

// TestTurnStartIncludesAllFeaturesAndMissingAttributionLikeRust mirrors Rust
// #45762: turn-start grouping includes every feature while keeping the full
// daily denominator, and a usage record without any breakdown is unavailable
// rather than zero.
func TestTurnStartIncludesAllFeaturesAndMissingAttributionLikeRust(t *testing.T) {
	date := analyticsDayForTest(t, "2026-01-09")
	response := analyticsData{Data: []analyticsRecord{{
		Date: "2026-01-09",
		Attribution: []analyticsUsageAttribution{
			{ThreadSource: stringPointer("user"), TurnTrigger: stringPointer("composer"), Value: 30},
			{ThreadSource: stringPointer("subagent"), Value: 70},
		},
	}}}
	got, err := history(response, AccountAnalyticsReportUsage, AccountAnalyticsGroupingTaskStart, date, date)
	if err != nil {
		t.Fatalf("history() error = %v", err)
	}
	want := &AccountAnalyticsHistory{
		Unit: AccountAnalyticsUnitRelativeUsage,
		Data: []AccountAnalyticsDay{{
			Date:  date,
			Total: 100,
			Values: []AccountAnalyticsValue{
				{Key: "start-composer", Label: "User messages", Value: 30},
				{Key: "unknown", Label: "Unknown", Value: 70},
			},
		}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("history = %#v, want %#v", got, want)
	}
	got, err = history(analyticsData{Data: []analyticsRecord{{Date: "2026-01-09"}}}, AccountAnalyticsReportUsage, AccountAnalyticsGroupingFeature, date, date)
	if err != nil || got != nil {
		t.Fatalf("missing attribution history = %#v, %v; want nil, nil", got, err)
	}
}

// TestSignedCreditsKeepDailyActivityWhenPeriodNetsToZeroLikeRust mirrors Rust
// #45740: credit reports preserve signed adjustments per day instead of hiding
// a day whose activity nets to zero.
func TestSignedCreditsKeepDailyActivityWhenPeriodNetsToZeroLikeRust(t *testing.T) {
	start := analyticsDayForTest(t, "2026-01-08")
	end := analyticsDayForTest(t, "2026-01-09")
	response := analyticsData{Data: []analyticsRecord{
		{Date: "2026-01-08", ProductSurface: stringPointer("cli"), CreditAmount: floatPointer(0.004)},
		{Date: "2026-01-09", ProductSurface: stringPointer("desktop_app"), CreditAmount: floatPointer(-0.004)},
	}}
	got, err := history(response, AccountAnalyticsReportCredits, AccountAnalyticsGroupingSurface, start, end)
	if err != nil {
		t.Fatalf("history() error = %v", err)
	}
	want := &AccountAnalyticsHistory{
		Unit: AccountAnalyticsUnitCredits,
		Data: []AccountAnalyticsDay{
			{Date: start, Total: 0.004, Values: []AccountAnalyticsValue{{Key: "cli", Label: "CLI", Value: 0.004}}},
			{Date: end, Total: -0.004, Values: []AccountAnalyticsValue{{Key: "desktop_app", Label: "Desktop app", Value: -0.004}}},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("history = %#v, want %#v", got, want)
	}
}

// TestMessageSurfacesMergeDesktopClientsAndUnmappedCountsLikeRust mirrors Rust
// #45740: both desktop clients fold into desktop_app and the difference to the
// authoritative total becomes "Other".
func TestMessageSurfacesMergeDesktopClientsAndUnmappedCountsLikeRust(t *testing.T) {
	date := analyticsDayForTest(t, "2026-01-09")
	response := analyticsData{Data: []analyticsRecord{{
		Date:   "2026-01-09",
		Totals: &analyticsCount{Turns: 10},
		Clients: []analyticsClient{
			{ClientID: "CODEX_DESKTOP_APP", Turns: 2},
			{ClientID: "CODEX_WORK_DESKTOP", Turns: 3},
			{ClientID: "new_client", Turns: 1},
		},
	}}}
	got, err := history(response, AccountAnalyticsReportMessages, AccountAnalyticsGroupingSurface, date, date)
	if err != nil || got == nil {
		t.Fatalf("history() = %#v, %v", got, err)
	}
	want := []AccountAnalyticsDay{{
		Date:  date,
		Total: 10,
		Values: []AccountAnalyticsValue{
			{Key: "desktop_app", Label: "Desktop app", Value: 5},
			{Key: "other", Label: "Other", Value: 5},
		},
	}}
	if !reflect.DeepEqual(got.Data, want) {
		t.Fatalf("history data = %#v, want %#v", got.Data, want)
	}
}

// TestEnterpriseCreditSeriesKeepLabelsSignedValuesAndFreshnessLikeRust mirrors
// Rust #45740: enterprise series labels win over the built-in label table and
// the freshness timestamp becomes Unix seconds.
func TestEnterpriseCreditSeriesKeepLabelsSignedValuesAndFreshnessLikeRust(t *testing.T) {
	date := analyticsDayForTest(t, "2026-01-09")
	freshness := "2026-01-09T10:00:00Z"
	response := analyticsData{
		DataFreshnessTS: &freshness,
		Series: []analyticsSeries{
			{Key: "medium", Label: "Medium"},
			{Key: "high", Label: "High"},
		},
		Data: []analyticsRecord{{
			Date:   "2026-01-09",
			Values: map[string]float64{"medium": 12.5, "high": -0.004},
		}},
	}
	got, err := history(response, AccountAnalyticsReportCredits, AccountAnalyticsGroupingReasoning, date, date)
	if err != nil {
		t.Fatalf("history() error = %v", err)
	}
	updatedAt := int64(1767952800)
	want := &AccountAnalyticsHistory{
		Unit:      AccountAnalyticsUnitCredits,
		UpdatedAt: &updatedAt,
		Data: []AccountAnalyticsDay{{
			Date:  date,
			Total: 12.496,
			Values: []AccountAnalyticsValue{
				{Key: "high", Label: "High", Value: -0.004},
				{Key: "medium", Label: "Medium", Value: 12.5},
			},
		}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("history = %#v, want %#v", got, want)
	}
}

// TestCreditTimestampsAndLegacyModelUsageFallbacksLikeRust mirrors Rust #45740:
// a full timestamp still parses through its date part, and legacy model usage
// falls back to the per-model credits amount.
func TestCreditTimestampsAndLegacyModelUsageFallbacksLikeRust(t *testing.T) {
	date := analyticsDayForTest(t, "2026-01-09")
	credits, err := history(analyticsData{Data: []analyticsRecord{{
		Date:           "2026-01-09T05:00:00Z",
		ProductSurface: stringPointer("cli"),
		CreditAmount:   floatPointer(12.5),
	}}}, AccountAnalyticsReportCredits, AccountAnalyticsGroupingSurface, date, date)
	if err != nil || credits == nil {
		t.Fatalf("credits history = %#v, %v", credits, err)
	}
	usage, err := history(analyticsData{Data: []analyticsRecord{{
		Date: "2026-01-09",
		Models: []analyticsModel{
			{Model: "model-a", Credits: floatPointer(12.5)},
		},
	}}}, AccountAnalyticsReportUsage, AccountAnalyticsGroupingModel, date, date)
	if err != nil || usage == nil {
		t.Fatalf("usage history = %#v, %v", usage, err)
	}
	if credits.Data[0].Total != 12.5 || usage.Data[0].Total != 12.5 || usage.Data[0].Values[0].Key != "model-a" {
		t.Fatalf("credits total = %v, usage = %#v", credits.Data[0].Total, usage.Data[0])
	}
}

// TestCountReportsPreserveExplicitZeroAndOmitUnreportedDaysLikeRust mirrors Rust
// #45740: count reports keep an explicit zero day and do not invent days inside
// the requested range.
func TestCountReportsPreserveExplicitZeroAndOmitUnreportedDaysLikeRust(t *testing.T) {
	start := analyticsDayForTest(t, "2026-01-08")
	end := analyticsDayForTest(t, "2026-01-10")
	tests := []struct {
		name     string
		report   AccountAnalyticsReport
		response analyticsData
	}{
		{"messages", AccountAnalyticsReportMessages, analyticsData{Data: []analyticsRecord{{Date: "2026-01-08", Totals: &analyticsCount{}}}}},
		{"plugins", AccountAnalyticsReportPlugins, analyticsData{Data: []analyticsRecord{{Date: "2026-01-08", PluginUsageOverviews: []analyticsTool{}}}}},
		{"skills", AccountAnalyticsReportSkills, analyticsData{Data: []analyticsRecord{{Date: "2026-01-08", SkillUsageOverviews: []analyticsTool{}}}}},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			got, err := history(testCase.response, testCase.report, AccountAnalyticsGroupingSurface, start, end)
			if err != nil {
				t.Fatalf("history() error = %v", err)
			}
			want := &AccountAnalyticsHistory{
				Unit: AccountAnalyticsUnitCount,
				Data: []AccountAnalyticsDay{{Date: start}},
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("history = %#v, want %#v", got, want)
			}
		})
	}
}

// TestLegacyConsumerHistoryKeepsDailyProductsAndModelsLikeRust mirrors Rust
// #45762: without attribution the legacy surface and model breakdowns are used,
// work_ surfaces merge into "work", and models under 1% of relative usage fold
// into "other".
func TestLegacyConsumerHistoryKeepsDailyProductsAndModelsLikeRust(t *testing.T) {
	date := analyticsDayForTest(t, "2026-09-01")
	relative := "relative"
	response := analyticsData{
		Units: &relative,
		Data: []analyticsRecord{{
			Date: "2026-09-01",
			ProductSurfaceUsageValues: map[string]float64{
				"work_desktop": 12, "work_web": 8, "cli": 30, "desktop_app": 50,
			},
			Models: []analyticsModel{
				{Model: "major", Credits: floatPointer(98.5)},
				{Model: "one-percent", Credits: floatPointer(1)},
				{Model: "tiny", Credits: floatPointer(0.3)},
				{Model: "OTHER", Credits: floatPointer(0.2)},
			},
		}},
	}
	surface, err := history(response, AccountAnalyticsReportUsage, AccountAnalyticsGroupingSurface, date, date)
	if err != nil || surface == nil {
		t.Fatalf("surface history = %#v, %v", surface, err)
	}
	wantSurface := []AccountAnalyticsValue{
		{Key: "codex", Label: "Codex", Value: 80},
		{Key: "work", Label: "Work", Value: 20},
	}
	if surface.Data[0].Total != 100 || !reflect.DeepEqual(surface.Data[0].Values, wantSurface) {
		t.Fatalf("surface day = %#v", surface.Data[0])
	}
	byModel, err := history(response, AccountAnalyticsReportUsage, AccountAnalyticsGroupingModel, date, date)
	if err != nil || byModel == nil {
		t.Fatalf("model history = %#v, %v", byModel, err)
	}
	wantModels := []AccountAnalyticsValue{
		{Key: "major", Label: "major", Value: 98.5},
		{Key: "one-percent", Label: "one-percent", Value: 1},
		{Key: "other", Label: "Other", Value: 0.5},
	}
	if !reflect.DeepEqual(byModel.Data[0].Values, wantModels) {
		t.Fatalf("model values = %#v, want %#v", byModel.Data[0].Values, wantModels)
	}
	creditsUnits := "credits"
	response.Units = &creditsUnits
	inCredits, err := history(response, AccountAnalyticsReportUsage, AccountAnalyticsGroupingModel, date, date)
	if err != nil || inCredits == nil {
		t.Fatalf("credits history = %#v, %v", inCredits, err)
	}
	if inCredits.Unit != AccountAnalyticsUnitCredits || len(inCredits.Data[0].Values) != 4 {
		t.Fatalf("credits history = %#v", inCredits)
	}
}

// TestCompleteAttributionWinsAndIncompleteIsNotMixedLikeRust mirrors Rust
// #45762: complete attribution replaces legacy surface/model rows and keeps
// relative usage even when the payload reports credits, while a response with
// any unattributed row does not mix the two sources.
func TestCompleteAttributionWinsAndIncompleteIsNotMixedLikeRust(t *testing.T) {
	date := analyticsDayForTest(t, "2026-09-01")
	credits := "credits"
	response := analyticsData{
		Units: &credits,
		Data: []analyticsRecord{{
			Date:                      "2026-09-01",
			ProductSurfaceUsageValues: map[string]float64{"cli": 999},
			Models:                    []analyticsModel{{Model: "legacy", Credits: floatPointer(999)}},
			Attribution: []analyticsUsageAttribution{
				{ThreadSource: stringPointer("user"), TurnTrigger: stringPointer("composer"), Surface: stringPointer("desktop_app"), Model: stringPointer("alpha"), Value: 30},
				{ThreadSource: stringPointer("subagent"), TurnTrigger: stringPointer("goal"), Surface: stringPointer("cli"), Model: stringPointer("tiny"), Value: 0.1},
			},
		}},
	}
	for _, grouping := range []AccountAnalyticsGrouping{
		AccountAnalyticsGroupingFeature,
		AccountAnalyticsGroupingModel,
		AccountAnalyticsGroupingSurface,
		AccountAnalyticsGroupingTaskStart,
	} {
		normalized, err := history(response, AccountAnalyticsReportUsage, grouping, date, date)
		if err != nil || normalized == nil {
			t.Fatalf("grouping %v history = %#v, %v", grouping, normalized, err)
		}
		if normalized.Unit != AccountAnalyticsUnitRelativeUsage || normalized.Data[0].Total != 30.1 || len(normalized.Data[0].Values) != 2 {
			t.Fatalf("grouping %v history = %#v", grouping, normalized)
		}
	}
	incomplete := analyticsData{Data: []analyticsRecord{
		{Date: "2026-09-01", Attribution: []analyticsUsageAttribution{}},
		{Date: "2026-09-02"},
	}}
	got, err := history(incomplete, AccountAnalyticsReportUsage, AccountAnalyticsGroupingFeature, date, analyticsDayForTest(t, "2026-09-02"))
	if err != nil || got != nil {
		t.Fatalf("incomplete attribution history = %#v, %v; want nil, nil", got, err)
	}
}

// TestDuplicateMessageDatesPreserveEachRecordsRemainderLikeRust mirrors Rust
// #45740: duplicate dates merge and each record's own remainder becomes "other"
// before summing.
func TestDuplicateMessageDatesPreserveEachRecordsRemainderLikeRust(t *testing.T) {
	date := analyticsDayForTest(t, "2026-01-09")
	record := analyticsRecord{
		Date:    "2026-01-09",
		Totals:  &analyticsCount{Turns: 10},
		Models:  []analyticsModel{{Model: "example", Turns: floatPointer(8)}},
		Clients: []analyticsClient{{ClientID: "CODEX_CLI", Turns: 8}},
	}
	for _, testCase := range []struct {
		grouping  AccountAnalyticsGrouping
		wantKey   string
		wantLabel string
	}{
		{AccountAnalyticsGroupingModel, "example", "example"},
		{AccountAnalyticsGroupingSurface, "cli", "CLI"},
	} {
		got, err := history(analyticsData{Data: []analyticsRecord{record, record}}, AccountAnalyticsReportMessages, testCase.grouping, date, date)
		if err != nil || got == nil {
			t.Fatalf("history() = %#v, %v", got, err)
		}
		want := AccountAnalyticsDay{
			Date:  date,
			Total: 20,
			Values: []AccountAnalyticsValue{
				{Key: testCase.wantKey, Label: testCase.wantLabel, Value: 16},
				{Key: "other", Label: "Other", Value: 4},
			},
		}
		if !reflect.DeepEqual(got.Data, []AccountAnalyticsDay{want}) {
			t.Fatalf("history data = %#v, want %#v", got.Data, want)
		}
	}
}

// TestUnsupportedMessageGroupingsFailEvenWithoutRecordsLikeRust mirrors Rust
// #45740: the grouping is validated before any record is read.
func TestUnsupportedMessageGroupingsFailEvenWithoutRecordsLikeRust(t *testing.T) {
	date := analyticsDayForTest(t, "2026-01-09")
	for _, grouping := range []AccountAnalyticsGrouping{
		AccountAnalyticsGroupingFeature,
		AccountAnalyticsGroupingTaskStart,
		AccountAnalyticsGroupingSpeed,
		AccountAnalyticsGroupingReasoning,
		AccountAnalyticsGroupingTokenType,
	} {
		for _, data := range []analyticsData{
			{},
			{Data: []analyticsRecord{{Date: "2026-01-09", Totals: &analyticsCount{Turns: 10}}}},
		} {
			if _, err := history(data, AccountAnalyticsReportMessages, grouping, date, date); err == nil || err.Error() != "Unsupported message count grouping." {
				t.Fatalf("grouping %d error = %v, want unsupported message grouping", grouping, err)
			}
		}
	}
}

// TestProjectedUsageResponseNormalizesLikeRust covers the typed projection used
// in production: an app-server usage response with model breakdown and
// freshness flows through historyForResponse.
func TestProjectedUsageResponseNormalizesLikeRust(t *testing.T) {
	date := analyticsDayForTest(t, "2026-09-01")
	units := "relative"
	freshness := "2026-09-01T10:00:00Z"
	response := chatgptapi.AnalyticsResponse{
		Kind: chatgptapi.AnalyticsReportUsage,
		Usage: &chatgptapi.DailyProductSurfaceUsageResponse{
			Units:           &units,
			DataFreshnessTS: &freshness,
			Data: []chatgptapi.DailyProductSurfaceUsage{{
				Date:                      "2026-09-01",
				ProductSurfaceUsageValues: map[string]float64{"cli": 40, "desktop_app": 60},
			}},
		},
	}
	got, err := historyForResponse(response, AccountAnalyticsReportUsage, AccountAnalyticsGroupingSurface, date, date)
	if err != nil || got == nil {
		t.Fatalf("historyForResponse() = %#v, %v", got, err)
	}
	if got.Unit != AccountAnalyticsUnitRelativeUsage || got.UpdatedAt == nil || *got.UpdatedAt != date.Add(10*time.Hour).Unix() {
		t.Fatalf("history = %#v", got)
	}
	want := []AccountAnalyticsValue{{Key: "codex", Label: "Codex", Value: 100}}
	if !reflect.DeepEqual(got.Data[0].Values, want) || got.Data[0].Total != 100 {
		t.Fatalf("day = %#v, want %#v", got.Data[0], want)
	}
}
