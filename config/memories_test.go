package config

import "testing"

func TestMemoriesConfigDefaultsAndClampsMatchRust(t *testing.T) {
	defaults := (*Config)(nil).Memories()
	if defaults != DefaultMemoriesConfig() {
		t.Fatalf("defaults = %+v", defaults)
	}
	extract := "extract-model"
	consolidate := "consolidate-model"
	cfg := &Config{Values: map[string]any{"memories": map[string]any{
		"no_memories_if_mcp_or_web_search":   true,
		"generate_memories":                  false,
		"use_memories":                       false,
		"dedicated_tools":                    true,
		"max_raw_memories_for_consolidation": int64(0),
		"max_unused_days":                    int64(500),
		"max_rollout_age_days":               int64(-1),
		"max_rollouts_per_startup":           int64(1000),
		"min_rollout_idle_hours":             int64(0),
		"min_rate_limit_remaining_percent":   int64(101),
		"extract_model":                      extract,
		"consolidation_model":                consolidate,
	}}}
	got := cfg.Memories()
	if !got.DisableOnExternalContext || got.GenerateMemories || got.UseMemories || !got.DedicatedTools {
		t.Fatalf("boolean settings = %+v", got)
	}
	if got.MaxRawMemoriesForConsolidation != 1 || got.MaxUnusedDays != 365 || got.MaxRolloutAgeDays != 0 || got.MaxRolloutsPerStartup != 128 || got.MinRolloutIdleHours != 1 || got.MinRateLimitRemainingPercent != 100 {
		t.Fatalf("clamped settings = %+v", got)
	}
	if got.ExtractModel == nil || *got.ExtractModel != extract || got.ConsolidationModel == nil || *got.ConsolidationModel != consolidate {
		t.Fatalf("model settings = %+v", got)
	}
}

func TestMemoriesConfigCanonicalDisableKeyOverridesAlias(t *testing.T) {
	cfg := &Config{Values: map[string]any{"memories": map[string]any{
		"no_memories_if_mcp_or_web_search": true,
		"disable_on_external_context":      false,
		"min_rate_limit_remaining_percent": int64(-1),
	}}}
	got := cfg.Memories()
	if got.DisableOnExternalContext || got.MinRateLimitRemainingPercent != 0 {
		t.Fatalf("settings = %+v", got)
	}
}
