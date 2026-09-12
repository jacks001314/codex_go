package config

import "testing"

// TestHideAgentReasoningLikeRust covers Rust's hide_agent_reasoning (default
// false; `codex exec` shows reasoning when it is false).
func TestHideAgentReasoningLikeRust(t *testing.T) {
	tests := []struct {
		name   string
		values map[string]any
		want   bool
	}{
		{name: "default", values: map[string]any{}, want: false},
		{name: "nil values", values: nil, want: false},
		{name: "explicit true", values: map[string]any{"hide_agent_reasoning": true}, want: true},
		{name: "explicit false", values: map[string]any{"hide_agent_reasoning": false}, want: false},
		{name: "wrong type falls back to false", values: map[string]any{"hide_agent_reasoning": "yes"}, want: false},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			cfg := &Config{Values: testCase.values}
			if got := cfg.HideAgentReasoning(); got != testCase.want {
				t.Fatalf("HideAgentReasoning() = %v, want %v", got, testCase.want)
			}
		})
	}
	var nilConfig *Config
	if nilConfig.HideAgentReasoning() {
		t.Fatal("nil config must not hide reasoning")
	}
}
