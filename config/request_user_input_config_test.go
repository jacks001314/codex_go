package config

import "testing"

func TestExperimentalRequestUserInputEnabledLikeRust(t *testing.T) {
	cases := []struct {
		name   string
		values map[string]any
		want   bool
	}{
		{name: "missing config defaults enabled", want: true},
		{name: "empty tools table defaults enabled", values: map[string]any{"tools": map[string]any{}}, want: true},
		{name: "enabled false", values: map[string]any{"tools": map[string]any{
			"experimental_request_user_input": map[string]any{"enabled": false},
		}}},
		{name: "enabled true", values: map[string]any{"tools": map[string]any{
			"experimental_request_user_input": map[string]any{"enabled": true},
		}}, want: true},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			config := &Config{Values: testCase.values}
			if got := config.ExperimentalRequestUserInputEnabled(); got != testCase.want {
				t.Fatalf("ExperimentalRequestUserInputEnabled() = %v, want %v", got, testCase.want)
			}
		})
	}
}
