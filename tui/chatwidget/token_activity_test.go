package chatwidget

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestTokenActivityViewAliasesParse(t *testing.T) {
	tests := map[string]TokenActivityView{
		"":           TokenActivityDaily,
		"day":        TokenActivityDaily,
		"daily":      TokenActivityDaily,
		"week":       TokenActivityWeekly,
		"weekly":     TokenActivityWeekly,
		"cumulative": TokenActivityCumulative,
	}
	for input, want := range tests {
		got, ok := ParseTokenActivityView(input)
		if !ok || got != want {
			t.Fatalf("ParseTokenActivityView(%q) = %q ok=%v, want %q", input, got, ok, want)
		}
	}
	if got, ok := ParseTokenActivityView("year"); ok || got != "" {
		t.Fatalf("ParseTokenActivityView(year) = %q ok=%v, want none", got, ok)
	}
}

func TestFormatTokensCompactMatchesRust(t *testing.T) {
	tests := map[int64]string{
		-5:                "0",
		0:                 "0",
		999:               "999",
		1_000:             "1K",
		1_234:             "1.23K",
		12_340:            "12.3K",
		123_400:           "123K",
		835_000_000:       "835M",
		21_400_000_000:    "21.4B",
		1_000_000_000_000: "1T",
	}
	for input, want := range tests {
		if got := FormatTokensCompact(input); got != want {
			t.Fatalf("FormatTokensCompact(%d) = %q, want %q", input, got, want)
		}
	}
}

func TestTokenActivitySummaryLines(t *testing.T) {
	lifetime := int64(21_400_000_000)
	peak := int64(835_000_000)
	longest := int64(13_920)
	currentStreak := int64(54)
	longestStreak := int64(54)
	summary := TokenActivitySummary{
		LifetimeTokens:        &lifetime,
		PeakDailyTokens:       &peak,
		LongestRunningTurnSec: &longest,
		CurrentStreakDays:     &currentStreak,
		LongestStreakDays:     &longestStreak,
	}

	wide := TokenActivitySummaryLines(summary, graphWidth(120))
	wantWide := []string{" Lifetime 21.4B · Peak 835M · Streak 54d · Longest task 3h 52m"}
	if !reflect.DeepEqual(wide, wantWide) {
		t.Fatalf("wide summary = %#v, want %#v", wide, wantWide)
	}

	tight := TokenActivitySummaryLines(summary, graphWidth(62))
	wantTight := []string{
		" Lifetime 21.4B · Peak 835M · Streak 54d",
		" Longest task 3h 52m",
	}
	if !reflect.DeepEqual(tight, wantTight) {
		t.Fatalf("tight summary = %#v, want %#v", tight, wantTight)
	}
}

func TestTokenActivityOptionalFormatting(t *testing.T) {
	current := int64(12)
	longest := int64(54)
	if got := FormatStreak(&current, &longest); got != "12d (best 54d)" {
		t.Fatalf("streak = %q", got)
	}
	if got := FormatStreak(nil, &longest); got != "- (best 54d)" {
		t.Fatalf("longest only streak = %q", got)
	}
	if got := FormatStreak(nil, nil); got != "-" {
		t.Fatalf("empty streak = %q", got)
	}

	seconds := int64(59)
	if got := FormatOptionalDuration(&seconds); got != "59s" {
		t.Fatalf("duration 59 = %q", got)
	}
	seconds = 125
	if got := FormatOptionalDuration(&seconds); got != "2m" {
		t.Fatalf("duration 125 = %q", got)
	}
	seconds = 7200
	if got := FormatOptionalDuration(&seconds); got != "2h" {
		t.Fatalf("duration 7200 = %q", got)
	}
}

func TestTokenActivityLinesLifecycle(t *testing.T) {
	if got := TokenActivityLines(TokenActivityDaily, NewTokenActivityLoadingState(), 80); !reflect.DeepEqual(got, []string{" Token activity", "   Loading..."}) {
		t.Fatalf("loading lines = %#v", got)
	}
	if got := TokenActivityLines(TokenActivityDaily, NewTokenActivityErrorState(), 80); !reflect.DeepEqual(got, []string{" Token activity", "   Token activity unavailable"}) {
		t.Fatalf("error lines = %#v", got)
	}

	response := TokenActivityResponse{}
	got := TokenActivityLines(TokenActivityWeekly, NewTokenActivityLoadedState(response, time.Date(2026, 5, 29, 0, 0, 0, 0, time.UTC)), 80)
	if len(got) < 4 || got[0] != " Token activity   last 12 months" || got[len(got)-1] != "   Token activity history unavailable" {
		t.Fatalf("loaded unavailable lines = %#v", got)
	}
}

func TestTokenActivityDailyValuesDuplicateDatesAndNegativeClamp(t *testing.T) {
	today := time.Date(2026, 5, 29, 0, 0, 0, 0, time.UTC)
	values := TokenActivityDailyValues([]TokenActivityDailyBucket{
		{StartDate: "2026-05-29", Tokens: 10},
		{StartDate: "2026-05-29", Tokens: 5},
		{StartDate: "2026-05-28", Tokens: -4},
		{StartDate: "2026-05-30", Tokens: 100},
		{StartDate: "not-a-date", Tokens: 100},
	}, today)
	var sum int64
	for _, value := range values {
		sum += value
	}
	if sum != 15 {
		t.Fatalf("daily values sum = %d, want 15", sum)
	}
}

func TestTokenActivityBarLevelsFillFromBottom(t *testing.T) {
	levels := tokenActivityBarLevels([]int64{0, 10})
	if !reflect.DeepEqual(levels[:tokenActivityDayCount], []int{0, 0, 0, 0, 0, 0, 0}) {
		t.Fatalf("empty bar levels = %#v", levels[:tokenActivityDayCount])
	}
	if !reflect.DeepEqual(levels[tokenActivityDayCount:], []int{4, 4, 4, 4, 4, 4, 4}) {
		t.Fatalf("full bar levels = %#v", levels[tokenActivityDayCount:])
	}
}

func TestTokenActivityChartLinesRenderDailyWeeklyAndCumulative(t *testing.T) {
	today := time.Date(2026, 5, 29, 0, 0, 0, 0, time.UTC)
	buckets := []TokenActivityDailyBucket{
		{StartDate: "2026-05-11", Tokens: 3},
		{StartDate: "2026-05-18", Tokens: 6},
		{StartDate: "2026-05-25", Tokens: 9},
	}

	daily := strings.Join(TokenActivityChartLines(TokenActivityDaily, buckets, today, 22), "\n")
	for _, want := range []string{"Apr", "May", " Su ", "   Less", "DAILY"} {
		if !strings.Contains(daily, want) {
			t.Fatalf("daily chart missing %q:\n%s", want, daily)
		}
	}

	weekly := strings.Join(TokenActivityChartLines(TokenActivityWeekly, buckets, today, 22), "\n")
	for _, want := range []string{"max ", "  0 ", "Each column = 1 week", "tallest 9", "WEEKLY"} {
		if !strings.Contains(weekly, want) {
			t.Fatalf("weekly chart missing %q:\n%s", want, weekly)
		}
	}

	cumulative := strings.Join(TokenActivityChartLines(TokenActivityCumulative, buckets, today, 22), "\n")
	for _, want := range []string{"Running total", "top 18", "CUMULATIVE"} {
		if !strings.Contains(cumulative, want) {
			t.Fatalf("cumulative chart missing %q:\n%s", want, cumulative)
		}
	}
}

func TestTokenActivityLoadedLinesRenderChartInsteadOfPending(t *testing.T) {
	today := time.Date(2026, 5, 29, 0, 0, 0, 0, time.UTC)
	buckets := []TokenActivityDailyBucket{
		{StartDate: "2026-05-25", Tokens: 9},
	}
	response := TokenActivityResponse{DailyUsageBuckets: &buckets}
	lines := strings.Join(TokenActivityLines(TokenActivityWeekly, NewTokenActivityLoadedState(response, today), 80), "\n")
	if strings.Contains(lines, "activity graph pending") {
		t.Fatalf("loaded chart still pending:\n%s", lines)
	}
	if !strings.Contains(lines, "Each column = 1 week") || !strings.Contains(lines, "WEEKLY") {
		t.Fatalf("loaded weekly chart missing caption/footer:\n%s", lines)
	}
}
