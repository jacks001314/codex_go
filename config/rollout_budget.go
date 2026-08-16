package config

import (
	"fmt"
	"math"
)

// RolloutBudgetConfig mirrors Rust's resolved rollout budget configuration
// (codex-rs core/src/config/mod.rs RolloutBudgetConfig), resolved from the
// `features.rollout_budget` table.
type RolloutBudgetConfig struct {
	LimitTokens               int64
	ReminderAtRemainingTokens []int64
	SamplingTokenWeight       float64
	PrefillTokenWeight        float64
}

// RolloutBudgetConfig resolves `features.rollout_budget` with the same
// semantics as Rust's resolve_rollout_budget_config:
//   - absent section (or disabled) -> nil, nil;
//   - enabled (bare `rollout_budget = true` or `enabled = true`) requires
//     limit_tokens and reminder_at_remaining_tokens with positivity bounds,
//     and finite non-negative weights (sampling/prefill default to 1.0).
func (c *Config) RolloutBudgetConfig() (*RolloutBudgetConfig, error) {
	if c == nil || c.Values == nil {
		return nil, nil
	}
	features, _ := c.Values["features"].(map[string]any)
	raw, ok := features["rollout_budget"]
	if !ok {
		return nil, nil
	}
	enabled := false
	var toml map[string]any
	switch v := raw.(type) {
	case bool:
		enabled = v
	case map[string]any:
		toml = v
		if value, ok := toml["enabled"].(bool); ok {
			enabled = value
		}
	default:
		return nil, fmt.Errorf("features.rollout_budget must be a boolean or a table")
	}
	if !enabled {
		return nil, nil
	}
	if toml == nil {
		return nil, fmt.Errorf("features.rollout_budget.limit_tokens is required when rollout_budget is enabled")
	}

	limit, ok := int64ConfigValue(toml["limit_tokens"])
	if !ok {
		return nil, fmt.Errorf("features.rollout_budget.limit_tokens is required when rollout_budget is enabled")
	}
	if limit <= 0 {
		return nil, fmt.Errorf("features.rollout_budget.limit_tokens must be positive")
	}
	reminders, ok := int64SliceConfigValue(toml["reminder_at_remaining_tokens"])
	if !ok {
		return nil, fmt.Errorf("features.rollout_budget.reminder_at_remaining_tokens is required when rollout_budget is enabled")
	}
	for _, reminder := range reminders {
		if reminder <= 0 || reminder >= limit {
			return nil, fmt.Errorf("features.rollout_budget.reminder_at_remaining_tokens must contain only positive values below limit_tokens")
		}
	}

	sampling := 1.0
	if value, ok := float64ConfigValue(toml["sampling_token_weight"]); ok {
		sampling = value
	}
	prefill := 1.0
	if value, ok := float64ConfigValue(toml["prefill_token_weight"]); ok {
		prefill = value
	}
	for _, weight := range []struct {
		name  string
		value float64
	}{{"sampling_token_weight", sampling}, {"prefill_token_weight", prefill}} {
		if math.IsNaN(weight.value) || math.IsInf(weight.value, 0) || weight.value < 0 {
			return nil, fmt.Errorf("features.rollout_budget.%s must be finite and non-negative", weight.name)
		}
	}
	return &RolloutBudgetConfig{
		LimitTokens:               limit,
		ReminderAtRemainingTokens: reminders,
		SamplingTokenWeight:       sampling,
		PrefillTokenWeight:        prefill,
	}, nil
}

func int64ConfigValue(value any) (int64, bool) {
	switch v := value.(type) {
	case int64:
		return v, true
	case int:
		return int64(v), true
	case float64:
		if v == math.Trunc(v) {
			return int64(v), true
		}
	}
	return 0, false
}

func int64SliceConfigValue(value any) ([]int64, bool) {
	values, ok := value.([]any)
	if !ok {
		return nil, false
	}
	out := make([]int64, 0, len(values))
	for _, item := range values {
		parsed, ok := int64ConfigValue(item)
		if !ok {
			return nil, false
		}
		out = append(out, parsed)
	}
	return out, true
}

func float64ConfigValue(value any) (float64, bool) {
	switch v := value.(type) {
	case float64:
		return v, true
	case int64:
		return float64(v), true
	case int:
		return float64(v), true
	}
	return 0, false
}
