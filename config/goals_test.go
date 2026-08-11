package config

import "testing"

func TestGoalsConfigParsesMaxGoalTokenBudget(t *testing.T) {
	cfg := &Config{Values: map[string]any{
		"goals": map[string]any{"max_goal_token_budget": int64(5000)},
	}}
	goals, err := cfg.GoalsConfig()
	if err != nil {
		t.Fatalf("GoalsConfig() error = %v", err)
	}
	if goals.MaxGoalTokenBudget == nil || *goals.MaxGoalTokenBudget != 5000 {
		t.Fatalf("MaxGoalTokenBudget = %#v, want 5000", goals.MaxGoalTokenBudget)
	}
}

func TestGoalsConfigDefaultsAndRejectsInvalid(t *testing.T) {
	if goals, err := (&Config{Values: map[string]any{}}).GoalsConfig(); err != nil || goals.MaxGoalTokenBudget != nil {
		t.Fatalf("default GoalsConfig() = %#v, %v", goals, err)
	}
	if goals, err := (&Config{Values: nil}).GoalsConfig(); err != nil || goals.MaxGoalTokenBudget != nil {
		t.Fatalf("nil config GoalsConfig() = %#v, %v", goals, err)
	}
	if _, err := (&Config{Values: map[string]any{
		"goals": map[string]any{"max_goal_token_budget": int64(-1)},
	}}).GoalsConfig(); err == nil {
		t.Fatal("expected negative budget error")
	}
	if _, err := (&Config{Values: map[string]any{
		"goals": map[string]any{"max_goal_token_budget": "many"},
	}}).GoalsConfig(); err == nil {
		t.Fatal("expected non-integer budget error")
	}
}
