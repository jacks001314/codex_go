package analytics

import (
	"reflect"
	"testing"
)

// TestTokensUseTextComponentsAcrossGroupingsLikeRust mirrors Rust #45741:
// token-type and model groupings share the daily text token total and the
// component labels stay in key order.
func TestTokensUseTextComponentsAcrossGroupingsLikeRust(t *testing.T) {
	date := analyticsDayForTest(t, "2026-09-01")
	response := analyticsData{Data: []analyticsRecord{{
		Date: "2026-09-01",
		Groups: []analyticsTokenGroup{{
			Dimensions:              map[string]string{"model": "gpt-5"},
			UncachedTextInputTokens: floatPointer(10),
			CachedTextInputTokens:   floatPointer(80),
			TextOutputTokens:        floatPointer(10),
		}},
	}}}
	byType, err := tokenHistory(response, AccountAnalyticsGroupingTokenType, date, date)
	if err != nil {
		t.Fatalf("tokenHistory(type) error = %v", err)
	}
	byModel, err := tokenHistory(response, AccountAnalyticsGroupingModel, date, date)
	if err != nil {
		t.Fatalf("tokenHistory(model) error = %v", err)
	}
	if byType.Unit != AccountAnalyticsUnitTokens || byType.Data[0].Total != 100 || byModel.Data[0].Total != 100 {
		t.Fatalf("byType = %#v, byModel = %#v", byType, byModel)
	}
	want := []AccountAnalyticsValue{
		{Key: "Cached input", Label: "Cached input", Value: 80},
		{Key: "Output", Label: "Output", Value: 10},
		{Key: "Uncached input", Label: "Uncached input", Value: 10},
	}
	if !reflect.DeepEqual(byType.Data[0].Values, want) {
		t.Fatalf("values = %#v, want %#v", byType.Data[0].Values, want)
	}
}

// TestTokenModelFilterRetainsComponentsAndFreshnessLikeRust mirrors Rust #45741:
// filtering to one model drops the other groups, keeps the component breakdown,
// and preserves the freshness timestamp.
func TestTokenModelFilterRetainsComponentsAndFreshnessLikeRust(t *testing.T) {
	date := analyticsDayForTest(t, "2026-09-01")
	freshness := "2026-09-02T00:00:00Z"
	response := analyticsData{
		DataFreshnessTS: &freshness,
		Data: []analyticsRecord{{
			Date: "2026-09-01",
			Groups: []analyticsTokenGroup{
				{Dimensions: map[string]string{"model": "alpha"}, UncachedTextInputTokens: floatPointer(10), CachedTextInputTokens: floatPointer(20), TextOutputTokens: floatPointer(30)},
				{Dimensions: map[string]string{"model": "beta"}, UncachedTextInputTokens: floatPointer(100), CachedTextInputTokens: floatPointer(200), TextOutputTokens: floatPointer(300)},
			},
			Models: []analyticsModel{{Model: "ignored", TextTotalTokens: floatPointer(1000)}},
		}},
	}
	selected, err := tokenFilteredHistory(response, AccountAnalyticsGroupingTokenType, date, date, stringPointer("alpha"))
	if err != nil {
		t.Fatalf("tokenFilteredHistory() error = %v", err)
	}
	want, err := tokenHistory(analyticsData{
		DataFreshnessTS: &freshness,
		Data: []analyticsRecord{{
			Date:   "2026-09-01",
			Models: []analyticsModel{{Model: "alpha", UncachedTextInputTokens: floatPointer(10), CachedTextInputTokens: floatPointer(20), TextOutputTokens: floatPointer(30)}},
		}},
	}, AccountAnalyticsGroupingTokenType, date, date)
	if err != nil {
		t.Fatalf("tokenHistory() error = %v", err)
	}
	if !reflect.DeepEqual(selected, want) {
		t.Fatalf("filtered = %#v, want %#v", selected, want)
	}
	unfiltered, err := tokenHistory(response, AccountAnalyticsGroupingTokenType, date, date)
	if err != nil {
		t.Fatalf("tokenHistory() error = %v", err)
	}
	if unfiltered.Data[0].Total != 660 {
		t.Fatalf("unfiltered total = %v, want 660", unfiltered.Data[0].Total)
	}
}

// TestModelTotalsUseReportedTotalLikeRust mirrors Rust #45741: a reported model
// total stands in for missing components, while the same record cannot be
// normalized by token type.
func TestModelTotalsUseReportedTotalLikeRust(t *testing.T) {
	date := analyticsDayForTest(t, "2026-09-01")
	response := analyticsData{Data: []analyticsRecord{{
		Date:   "2026-09-01",
		Models: []analyticsModel{{Model: "alpha", TextTotalTokens: floatPointer(123)}},
	}}}
	got, err := tokenHistory(response, AccountAnalyticsGroupingModel, date, date)
	if err != nil {
		t.Fatalf("tokenHistory(model) error = %v", err)
	}
	want := []AccountAnalyticsDay{{
		Date:   date,
		Total:  123,
		Values: []AccountAnalyticsValue{{Key: "alpha", Label: "alpha", Value: 123}},
	}}
	if !reflect.DeepEqual(got.Data, want) {
		t.Fatalf("history data = %#v, want %#v", got.Data, want)
	}
	if _, err := tokenHistory(response, AccountAnalyticsGroupingTokenType, date, date); err == nil || err.Error() != "Token counts were not reported." {
		t.Fatalf("token-type error = %v, want missing counts", err)
	}
}

// TestExplicitZeroModelTotalIsReportedLikeRust mirrors Rust #45741: an explicit
// zero total is kept for an idle model, and a model with components but a zero
// total still reports its component counts.
func TestExplicitZeroModelTotalIsReportedLikeRust(t *testing.T) {
	date := analyticsDayForTest(t, "2026-09-01")
	response := analyticsData{Data: []analyticsRecord{{
		Date: "2026-09-01",
		Models: []analyticsModel{
			{Model: "idle", TextTotalTokens: floatPointer(0)},
			{Model: "active", TextTotalTokens: floatPointer(0), TextOutputTokens: floatPointer(5)},
		},
	}}}
	got, err := tokenHistory(response, AccountAnalyticsGroupingModel, date, date)
	if err != nil {
		t.Fatalf("tokenHistory() error = %v", err)
	}
	want := AccountAnalyticsHistory{
		Unit: AccountAnalyticsUnitTokens,
		Data: []AccountAnalyticsDay{{
			Date:  date,
			Total: 5,
			Values: []AccountAnalyticsValue{
				{Key: "active", Label: "active", Value: 5},
				{Key: "idle", Label: "idle", Value: 0},
			},
		}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("history = %#v, want %#v", got, want)
	}
}

// TestAnalyticsCreditFormattingKeepsPrecisionLikeRust mirrors Rust #45741's
// credit display helpers: tiny adjustments stay visible, ordinary amounts align
// to two decimals, and integer millionths never round through float.
func TestAnalyticsCreditFormattingKeepsPrecisionLikeRust(t *testing.T) {
	amounts := []struct {
		value float64
		want  string
	}{
		{753.71, "753.71"},
		{411.12, "411.12"},
		{47.4, "47.40"},
		{0, "0.00"},
		{1212.224, "1,212.22"},
		{-0.004, "-0.004"},
		{-0.000004, "-0.000004"},
	}
	for _, testCase := range amounts {
		if got := analyticsCreditAmount(testCase.value); got != testCase.want {
			t.Fatalf("credit amount %v = %q, want %q", testCase.value, got, testCase.want)
		}
	}
	credits := []struct {
		micros int64
		want   string
	}{
		{0, "0.00"},
		{-4, "-0.000004"},
		{999999999, "1,000.00"},
		{19350041555, "19,350.04"},
		{-9223372036854775808, "-9,223,372,036,854.78"},
		{9223372036854775807, "9,223,372,036,854.78"},
	}
	for _, testCase := range credits {
		if got := analyticsCredits(testCase.micros); got != testCase.want {
			t.Fatalf("credits %d = %q, want %q", testCase.micros, got, testCase.want)
		}
	}
	plain := []struct {
		value float64
		want  string
	}{
		{12746.441206, "12,746.44"},
		{-1000.125, "-1,000.12"},
		{999.999, "1,000"},
		{0, "0"},
		{-0.000004, "-0.000004"},
		{0.009, "0.009"},
		{7.5, "7.5"},
	}
	for _, testCase := range plain {
		if got := analyticsAmount(testCase.value); got != testCase.want {
			t.Fatalf("amount %v = %q, want %q", testCase.value, got, testCase.want)
		}
	}
	if got := analyticsShortDate("2026-01-09"); got != "Jan 9" {
		t.Fatalf("short date = %q, want Jan 9", got)
	}
	if got := analyticsShortDate("not-a-date"); got != "not-a-date" {
		t.Fatalf("short date fallback = %q", got)
	}
}
