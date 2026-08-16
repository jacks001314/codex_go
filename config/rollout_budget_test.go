package config

import (
	"reflect"
	"strings"
	"testing"
)

// These tests mirror Rust core/src/config/config_tests.rs
// (load_config_resolves_rollout_budget /
// load_config_rejects_enabled_rollout_budget_without_limit).

func TestRolloutBudgetConfigResolvesLikeRust(t *testing.T) {
	cfg := &Config{Values: map[string]any{
		"features": map[string]any{
			"rollout_budget": map[string]any{
				"enabled":                      true,
				"limit_tokens":                 int64(100000),
				"reminder_at_remaining_tokens": []any{int64(50000), int64(25000), int64(10000)},
				"sampling_token_weight":        1.0,
				"prefill_token_weight":         0.1,
			},
		},
	}}
	got, err := cfg.RolloutBudgetConfig()
	if err != nil {
		t.Fatalf("RolloutBudgetConfig() error = %v", err)
	}
	want := &RolloutBudgetConfig{
		LimitTokens:               100000,
		ReminderAtRemainingTokens: []int64{50000, 25000, 10000},
		SamplingTokenWeight:       1.0,
		PrefillTokenWeight:        0.1,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("RolloutBudgetConfig() = %#v, want %#v", got, want)
	}
}

func TestRolloutBudgetConfigDefaultsWeightsLikeRust(t *testing.T) {
	cfg := &Config{Values: map[string]any{
		"features": map[string]any{
			"rollout_budget": map[string]any{
				"enabled":                      true,
				"limit_tokens":                 int64(1000),
				"reminder_at_remaining_tokens": []any{int64(500)},
			},
		},
	}}
	got, err := cfg.RolloutBudgetConfig()
	if err != nil {
		t.Fatalf("RolloutBudgetConfig() error = %v", err)
	}
	if got.SamplingTokenWeight != 1.0 || got.PrefillTokenWeight != 1.0 {
		t.Fatalf("weights = %v/%v, want 1.0/1.0 defaults", got.SamplingTokenWeight, got.PrefillTokenWeight)
	}
}

func TestRolloutBudgetConfigDisabledReturnsNilLikeRust(t *testing.T) {
	cfg := &Config{Values: map[string]any{
		"features": map[string]any{
			"rollout_budget": map[string]any{
				"enabled":                      false,
				"limit_tokens":                 int64(1000),
				"reminder_at_remaining_tokens": []any{int64(500)},
			},
		},
	}}
	got, err := cfg.RolloutBudgetConfig()
	if err != nil || got != nil {
		t.Fatalf("disabled RolloutBudgetConfig() = %#v, %v; want nil, nil", got, err)
	}
	if cfg, err := (&Config{Values: map[string]any{}}).RolloutBudgetConfig(); cfg != nil || err != nil {
		t.Fatalf("absent section RolloutBudgetConfig() = %#v, %v; want nil, nil", cfg, err)
	}
}

func TestRolloutBudgetConfigRejectsEnabledWithoutLimitLikeRust(t *testing.T) {
	for _, features := range []map[string]any{
		{"rollout_budget": true},
		{"rollout_budget": map[string]any{"enabled": true}},
	} {
		cfg := &Config{Values: map[string]any{"features": features}}
		_, err := cfg.RolloutBudgetConfig()
		if err == nil || !strings.Contains(err.Error(), "features.rollout_budget.limit_tokens is required") {
			t.Fatalf("enabled without limit error = %v, want limit_tokens required", err)
		}
	}
}

func TestRolloutBudgetConfigRejectsInvalidValuesLikeRust(t *testing.T) {
	cases := []struct {
		name string
		toml map[string]any
		want string
	}{
		{"limit zero", map[string]any{"enabled": true, "limit_tokens": int64(0), "reminder_at_remaining_tokens": []any{int64(500)}}, "must be positive"},
		{"reminder missing", map[string]any{"enabled": true, "limit_tokens": int64(1000)}, "reminder_at_remaining_tokens is required"},
		{"reminder above limit", map[string]any{"enabled": true, "limit_tokens": int64(1000), "reminder_at_remaining_tokens": []any{int64(2000)}}, "positive values below limit_tokens"},
		{"negative weight", map[string]any{"enabled": true, "limit_tokens": int64(1000), "reminder_at_remaining_tokens": []any{int64(500)}, "sampling_token_weight": -1.0}, "finite and non-negative"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &Config{Values: map[string]any{"features": map[string]any{"rollout_budget": tc.toml}}}
			_, err := cfg.RolloutBudgetConfig()
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want containing %q", err, tc.want)
			}
		})
	}
}

// TestShowRawAgentReasoningAccessor pins the Rust show_raw_agent_reasoning
// default (false): absent or non-bool values hide raw reasoning; an explicit
// true surfaces it.
func TestShowRawAgentReasoningAccessor(t *testing.T) {
	absent := &Config{Values: map[string]any{}}
	if absent.ShowRawAgentReasoning() {
		t.Fatal("absent show_raw_agent_reasoning should default false like Rust")
	}
	enabled := &Config{Values: map[string]any{"show_raw_agent_reasoning": true}}
	if !enabled.ShowRawAgentReasoning() {
		t.Fatal("show_raw_agent_reasoning=true should report true")
	}
	disabled := &Config{Values: map[string]any{"show_raw_agent_reasoning": false}}
	if disabled.ShowRawAgentReasoning() {
		t.Fatal("show_raw_agent_reasoning=false should report false")
	}
}
