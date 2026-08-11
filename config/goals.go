package config

import "fmt"

// GoalsConfig holds goal-related settings from the `goals` configuration
// table (mirrors the Rust GoalsToml / Config.max_goal_token_budget).
type GoalsConfig struct {
	// MaxGoalTokenBudget is the maximum token budget allowed for a goal and
	// the default budget for new goals and null budget resets.
	MaxGoalTokenBudget *int64
}

func (c *Config) GoalsConfig() (*GoalsConfig, error) {
	out := &GoalsConfig{}
	if c == nil || c.Values == nil {
		return out, nil
	}
	goals, ok := c.Values["goals"].(map[string]any)
	if !ok {
		return out, nil
	}
	raw, exists := goals["max_goal_token_budget"]
	if !exists {
		return out, nil
	}
	value, ok := configInt64(raw)
	if !ok || value <= 0 {
		return nil, fmt.Errorf("goals.max_goal_token_budget must be a positive integer")
	}
	out.MaxGoalTokenBudget = &value
	return out, nil
}

func configInt64(value any) (int64, bool) {
	switch typed := value.(type) {
	case int64:
		return typed, true
	case int:
		return int64(typed), true
	case float64:
		if typed == float64(int64(typed)) {
			return int64(typed), true
		}
	}
	return 0, false
}
