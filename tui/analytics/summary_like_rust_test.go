package analytics

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"codex_go/auth"
	"codex_go/chatgptapi"
)

func summaryTestStringPtr(value string) *string { return &value }

func summaryTestIntPtr(value int64) *int64 { return &value }

func summaryTestNumber(value string) *json.Number {
	number := json.Number(value)
	return &number
}

func summaryTestProfile() *chatgptapi.AccountProfile {
	return &chatgptapi.AccountProfile{
		Profile: &chatgptapi.ProfileIdentity{
			DisplayName: summaryTestStringPtr("Test user"),
			Username:    summaryTestStringPtr("@test.user"),
		},
		Metadata: &chatgptapi.ProfileMetadata{
			StatsAsOf:  summaryTestStringPtr("2026-09-09"),
			StatsError: summaryTestStringPtr("partial upstream data"),
		},
		Stats: chatgptapi.ProfileStats{
			TokenUsageProfileStats: chatgptapi.TokenUsageProfileStats{
				LifetimeTokens:        summaryTestIntPtr(142_300_000_000),
				PeakDailyTokens:       summaryTestIntPtr(7_000_000_000),
				LongestRunningTurnSec: summaryTestIntPtr(158_760),
				CurrentStreakDays:     summaryTestIntPtr(158),
				LongestStreakDays:     summaryTestIntPtr(160),
				DailyUsageBuckets: &[]chatgptapi.TokenUsageProfileDailyBucket{
					{StartDate: "2026-09-09", Tokens: 42},
				},
			},
			FastModeUsagePercentage:           summaryTestNumber("37.5"),
			MostUsedReasoningEffort:           summaryTestStringPtr("high"),
			MostUsedReasoningEffortPercentage: summaryTestNumber("26"),
			UniqueSkillsUsed:                  summaryTestIntPtr(147),
			TotalSkillsUsed:                   summaryTestIntPtr(13_190),
			TotalThreads:                      summaryTestIntPtr(0),
			TopInvocations: &[]chatgptapi.ProfileInvocation{
				{Kind: chatgptapi.ProfileInvocationUnknown},
				{Kind: chatgptapi.ProfileInvocationSkill, SkillName: summaryTestStringPtr("review"), UsageCount: summaryTestIntPtr(12)},
				{Kind: chatgptapi.ProfileInvocationPlugin, PluginName: summaryTestStringPtr("github"), UsageCount: summaryTestIntPtr(34)},
			},
		},
	}
}

func summaryContains(lines []string, want string) bool {
	for _, line := range lines {
		if strings.Contains(line, want) {
			return true
		}
	}
	return false
}

func TestAnalyticsSummaryRendersProfileLikeRust(t *testing.T) {
	plan := auth.PlanPro
	lines := AnalyticsSummaryLines(AnalyticsSummaryOptions{
		Profile: summaryTestProfile(),
		Ready:   true,
		Plan:    &plan,
		Zoomed:  true,
		EndDate: time.Date(2026, 9, 9, 0, 0, 0, 0, time.UTC),
		Width:   100,
	})
	for _, want := range []string{
		"Test user",
		"@test.user",
		"Pro 200",
		"Lifetime tokens",
		"142.3B",
		"Peak tokens",
		"7B",
		"Longest chat",
		"44h 6m",
		"Current streak",
		"158 days",
		"Longest streak",
		"160 days",
		"Token activity",
		"Token activity",
		"Daily",
		"Last 12 months",
		"Activity insights",
		"Fast Mode",
		"38%",
		"Most used reasoning",
		"high · 26%",
		"Skills explored",
		"147",
		"Total skills used",
		"13,190",
		"Total chats",
		"Most used plugins and skills",
		"@github",
		"34 runs",
		"$review",
		"12 runs",
		"Statistics as of Sep 9, 2026",
		"Some profile statistics are unavailable.",
	} {
		if !summaryContains(lines, want) {
			t.Fatalf("summary missing %q:\n%s", want, strings.Join(lines, "\n"))
		}
	}
	if summaryContains(lines, "Test user ") {
		t.Fatalf("name should be trimmed:\n%s", strings.Join(lines, "\n"))
	}
}

func TestAnalyticsSummaryNarrowLayoutStacksFieldsLikeRust(t *testing.T) {
	narrow := AnalyticsSummaryLines(AnalyticsSummaryOptions{
		Profile: summaryTestProfile(),
		Ready:   true,
		Zoomed:  true,
		EndDate: time.Date(2026, 9, 9, 0, 0, 0, 0, time.UTC),
		Width:   30,
	})
	if !summaryContains(narrow, "Lifetime tokens") || !summaryContains(narrow, "142.3B") {
		t.Fatalf("narrow summary missing fields:\n%s", strings.Join(narrow, "\n"))
	}
	for _, line := range narrow {
		if analyticsDisplayWidth(line) > 30 {
			t.Fatalf("narrow line %q wider than 30", line)
		}
	}
	if summaryContains(narrow, "Token activity  Daily") {
		t.Fatalf("narrow header should stack the controls:\n%s", strings.Join(narrow, "\n"))
	}
	if !summaryContains(narrow, "Daily") {
		t.Fatalf("narrow controls missing:\n%s", strings.Join(narrow, "\n"))
	}
	overview := AnalyticsSummaryLines(AnalyticsSummaryOptions{
		Profile: summaryTestProfile(),
		Ready:   true,
		Zoomed:  false,
		Width:   60,
	})
	if summaryContains(overview, "Token activity") || summaryContains(overview, "Activity insights") {
		t.Fatalf("overview card kept zoomed sections:\n%s", strings.Join(overview, "\n"))
	}
	if !summaryContains(overview, "Lifetime tokens") || !summaryContains(overview, "142.3B") {
		t.Fatalf("overview card missing totals:\n%s", strings.Join(overview, "\n"))
	}
}

func TestAnalyticsSummaryMissingDataKeepsDistinctMessagesLikeRust(t *testing.T) {
	unavailable := AnalyticsSummaryLines(AnalyticsSummaryOptions{Width: 80})
	if len(unavailable) != 1 || unavailable[0] != "Profile statistics unavailable." {
		t.Fatalf("unavailable = %#v", unavailable)
	}
	custom := AnalyticsSummaryLines(AnalyticsSummaryOptions{Message: "Request failed. Press R to retry.", Width: 80})
	if len(custom) != 1 || custom[0] != "Request failed. Press R to retry." {
		t.Fatalf("custom = %#v", custom)
	}
	profile := summaryTestProfile()
	profile.Stats.DailyUsageBuckets = nil
	profile.Stats.TopInvocations = nil
	lines := AnalyticsSummaryLines(AnalyticsSummaryOptions{Profile: profile, Ready: true, Zoomed: true, Width: 60})
	if !summaryContains(lines, "Token activity history unavailable") {
		t.Fatalf("missing chart message:\n%s", strings.Join(lines, "\n"))
	}
	if !summaryContains(lines, "Plugin and skill usage unavailable.") {
		t.Fatalf("missing invocation message:\n%s", strings.Join(lines, "\n"))
	}
	empty := []chatgptapi.ProfileInvocation{}
	profile.Stats.TopInvocations = &empty
	lines = AnalyticsSummaryLines(AnalyticsSummaryOptions{Profile: profile, Ready: true, Zoomed: true, Width: 60})
	if !summaryContains(lines, "No reported plugins or skills.") {
		t.Fatalf("empty invocation message:\n%s", strings.Join(lines, "\n"))
	}
}

func TestAnalyticsSummaryMetricFormattingLikeRust(t *testing.T) {
	profile := summaryTestProfile()
	profile.Profile = nil
	profile.Metadata = nil
	profile.Stats.DailyUsageBuckets = nil
	profile.Stats.FastModeUsagePercentage = summaryTestNumber("101")
	profile.Stats.MostUsedReasoningEffort = nil
	profile.Stats.MostUsedReasoningEffortPercentage = nil
	profile.Stats.UniqueSkillsUsed = nil
	profile.Stats.TotalSkillsUsed = nil
	profile.Stats.TotalThreads = nil
	profile.Stats.TopInvocations = nil
	lines := AnalyticsSummaryLines(AnalyticsSummaryOptions{Profile: profile, Ready: true, Zoomed: true, Width: 60})
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "Fast Mode") || !strings.Contains(joined, "—") {
		t.Fatalf("unbounded percentage should render a dash:\n%s", joined)
	}
	for _, want := range []string{
		"Most used reasoning",
		"Skills explored",
		"Total skills used",
		"Total chats",
		"Plugin and skill usage unavailable.",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing %q:\n%s", want, joined)
		}
	}
	for _, line := range lines {
		if strings.Contains(line, "101%") {
			t.Fatalf("percentage above 100 leaked:\n%s", joined)
		}
	}
}

func TestAnalyticsCompactAmountPromotesBeforeRoundingLikeRust(t *testing.T) {
	cases := map[float64]string{
		0:                 "0",
		999:               "999",
		1_500:             "1.5K",
		999_949:           "999.9K",
		999_950:           "1M",
		142_300_000_000:   "142.3B",
		1_000_000_000_000: "1T",
	}
	for value, want := range cases {
		if got := analyticsCompactAmount(value); got != want {
			t.Fatalf("compact(%v) = %q, want %q", value, got, want)
		}
	}
}

func TestAnalyticsSummaryInvocationLimitAndUnknownKindsLikeRust(t *testing.T) {
	profile := summaryTestProfile()
	profile.Stats.DailyUsageBuckets = nil
	invocations := []chatgptapi.ProfileInvocation{
		{Kind: chatgptapi.ProfileInvocationUnknown, PluginName: summaryTestStringPtr("ignored")},
	}
	for index := 0; index < 7; index++ {
		invocations = append(invocations, chatgptapi.ProfileInvocation{
			Kind:       chatgptapi.ProfileInvocationPlugin,
			PluginName: summaryTestStringPtr("plugin" + string(rune('a'+index))),
			UsageCount: summaryTestIntPtr(1),
		})
	}
	profile.Stats.TopInvocations = &invocations
	lines := AnalyticsSummaryLines(AnalyticsSummaryOptions{Profile: profile, Ready: true, Zoomed: true, Width: 80})
	joined := strings.Join(lines, "\n")
	if strings.Contains(joined, "ignored") {
		t.Fatalf("unknown invocation rendered:\n%s", joined)
	}
	if strings.Contains(joined, "pluging") {
		t.Fatalf("more than five invocations rendered:\n%s", joined)
	}
	if strings.Count(joined, "1 run ")+strings.Count(joined, "1 run\n") != 5 {
		t.Fatalf("want five one-run rows:\n%s", joined)
	}
}

// Rust #47971 recognizes Pro Max, and #49079 moved the summary plan label onto
// SubscriptionDisplay::Analytics, so the Pro tiers render as 100/200/500.
func TestAnalyticsPlanLabelMatchesSubscriptionAnalyticsLabels(t *testing.T) {
	cases := map[auth.PlanType]string{
		auth.PlanPro:     "Pro 200",
		auth.PlanProlite: "Pro 100",
		auth.PlanProMax:  "Pro 500",
	}
	for plan, want := range cases {
		if got := analyticsPlanLabel(plan); got != want {
			t.Fatalf("analyticsPlanLabel(%q) = %q, want %q", plan, got, want)
		}
	}
}
