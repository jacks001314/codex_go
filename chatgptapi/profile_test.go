package chatgptapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGetAccountProfileKeepsMissingZeroAndFutureInvocationsDistinct(t *testing.T) {
	bodies := []string{
		`{"stats":{}}`,
		`{
			"profile": {"display_name": "Test user", "username": "test.user"},
			"metadata": {"stats_as_of": "2026-09-09", "stats_error": "partial upstream data"},
			"stats": {
				"lifetime_tokens": 142300000000,
				"peak_daily_tokens": 7000000000,
				"longest_running_turn_sec": 158760,
				"current_streak_days": 158,
				"longest_streak_days": 160,
				"daily_usage_buckets": [{"start_date": "2026-09-09", "tokens": 42}],
				"fast_mode_usage_percentage": 37.5,
				"most_used_reasoning_effort": "high",
				"most_used_reasoning_effort_percentage": 26,
				"unique_skills_used": 147,
				"total_skills_used": 13190,
				"total_threads": 0,
				"top_invocations": [
					{"type": "future_kind"},
					{"type": "skill", "skill_name": "review", "usage_count": 12},
					{"type": "plugin", "plugin_name": "github", "usage_count": 34}
				]
			}
		}`,
	}
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		if r.Method != http.MethodGet {
			t.Fatalf("method = %q", r.Method)
		}
		if len(paths) > len(bodies) {
			t.Fatalf("unexpected request %d", len(paths))
		}
		_, _ = io.WriteString(w, bodies[len(paths)-1])
	}))
	defer server.Close()
	client := NewCloudClient(&CloudClientOptions{BaseURL: server.URL})

	empty, err := client.GetAccountProfile(context.Background())
	if err != nil {
		t.Fatalf("GetAccountProfile(empty) error = %v", err)
	}
	if empty.Profile != nil || empty.Metadata != nil {
		t.Fatalf("empty profile = %+v", empty)
	}
	if empty.Stats.LifetimeTokens != nil || empty.Stats.DailyUsageBuckets != nil ||
		empty.Stats.TopInvocations != nil || empty.Stats.TotalThreads != nil ||
		empty.Stats.FastModeUsagePercentage != nil {
		t.Fatalf("empty stats = %+v", empty.Stats)
	}

	full, err := client.GetAccountProfile(context.Background())
	if err != nil {
		t.Fatalf("GetAccountProfile(full) error = %v", err)
	}
	if full.Profile == nil || full.Profile.DisplayName == nil || *full.Profile.DisplayName != "Test user" ||
		full.Profile.Username == nil || *full.Profile.Username != "test.user" {
		t.Fatalf("profile = %+v", full.Profile)
	}
	if full.Metadata == nil || full.Metadata.StatsAsOf == nil || *full.Metadata.StatsAsOf != "2026-09-09" ||
		full.Metadata.StatsError == nil || *full.Metadata.StatsError != "partial upstream data" {
		t.Fatalf("metadata = %+v", full.Metadata)
	}
	stats := full.Stats
	if stats.LifetimeTokens == nil || *stats.LifetimeTokens != 142_300_000_000 {
		t.Fatalf("lifetime tokens = %+v", stats.LifetimeTokens)
	}
	if stats.DailyUsageBuckets == nil || len(*stats.DailyUsageBuckets) != 1 ||
		(*stats.DailyUsageBuckets)[0].StartDate != "2026-09-09" || (*stats.DailyUsageBuckets)[0].Tokens != 42 {
		t.Fatalf("buckets = %+v", stats.DailyUsageBuckets)
	}
	if stats.FastModeUsagePercentage == nil || stats.FastModeUsagePercentage.String() != "37.5" {
		t.Fatalf("fast mode = %+v", stats.FastModeUsagePercentage)
	}
	if stats.MostUsedReasoningEffort == nil || *stats.MostUsedReasoningEffort != "high" ||
		stats.MostUsedReasoningEffortPercentage == nil || stats.MostUsedReasoningEffortPercentage.String() != "26" {
		t.Fatalf("reasoning effort = %+v %+v", stats.MostUsedReasoningEffort, stats.MostUsedReasoningEffortPercentage)
	}
	if stats.UniqueSkillsUsed == nil || *stats.UniqueSkillsUsed != 147 ||
		stats.TotalSkillsUsed == nil || *stats.TotalSkillsUsed != 13_190 ||
		stats.TotalThreads == nil || *stats.TotalThreads != 0 {
		t.Fatalf("skill totals = %+v", stats)
	}
	if stats.TopInvocations == nil || len(*stats.TopInvocations) != 3 {
		t.Fatalf("invocations = %+v", stats.TopInvocations)
	}
	invocations := *stats.TopInvocations
	if invocations[0].Kind != ProfileInvocationUnknown || invocations[0].PluginName != nil ||
		invocations[0].SkillName != nil || invocations[0].UsageCount != nil {
		t.Fatalf("future invocation = %+v", invocations[0])
	}
	if invocations[1].Kind != ProfileInvocationSkill || invocations[1].SkillName == nil ||
		*invocations[1].SkillName != "review" || *invocations[1].UsageCount != 12 {
		t.Fatalf("skill invocation = %+v", invocations[1])
	}
	if invocations[2].Kind != ProfileInvocationPlugin || invocations[2].PluginName == nil ||
		*invocations[2].PluginName != "github" || *invocations[2].UsageCount != 34 {
		t.Fatalf("plugin invocation = %+v", invocations[2])
	}
	if len(paths) != 2 {
		t.Fatalf("paths = %#v", paths)
	}
	for _, path := range paths {
		if path != "/api/codex/profiles/me" {
			t.Fatalf("path = %q", path)
		}
	}
}

func TestGetAccountProfileRejectsInvalidPayloads(t *testing.T) {
	for _, body := range []string{
		`private invalid payload`,
		``,
		`{}`,
		`{"profile": {"display_name": 7}}`,
	} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.WriteString(w, body)
		}))
		client := NewCloudClient(&CloudClientOptions{BaseURL: server.URL})
		_, err := client.GetAccountProfile(context.Background())
		if err == nil || err.Error() != accountProfileResponseMessage {
			server.Close()
			t.Fatalf("body %q error = %v", body, err)
		}
		server.Close()
	}
}

func TestProfileInvocationKindMapsUnknown(t *testing.T) {
	var kind ProfileInvocationKind
	if err := json.Unmarshal([]byte(`"skill"`), &kind); err != nil || kind != ProfileInvocationSkill {
		t.Fatalf("kind = %q err = %v", kind, err)
	}
	if err := json.Unmarshal([]byte(`"future_kind"`), &kind); err != nil || kind != ProfileInvocationUnknown {
		t.Fatalf("kind = %q err = %v", kind, err)
	}
	if err := json.Unmarshal([]byte(`7`), &kind); err == nil {
		t.Fatal("non-string kind accepted")
	}
}
