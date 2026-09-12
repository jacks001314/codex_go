package config

import "testing"

// TestBackgroundTerminalMaxTimeoutLikeRust covers Rust's
// `background_terminal_max_timeout` accessor (default 300000 ms, mirroring
// config/defaults.toml; the minimum-yield clamp lives in the caller).
func TestBackgroundTerminalMaxTimeoutLikeRust(t *testing.T) {
	tests := []struct {
		name   string
		values map[string]any
		want   uint64
	}{
		{name: "default", values: map[string]any{}, want: DefaultBackgroundTerminalMaxTimeoutMS},
		{name: "nil values", values: nil, want: DefaultBackgroundTerminalMaxTimeoutMS},
		{name: "int", values: map[string]any{"background_terminal_max_timeout": 60_000}, want: 60_000},
		{name: "int64", values: map[string]any{"background_terminal_max_timeout": int64(90_000)}, want: 90_000},
		{name: "float", values: map[string]any{"background_terminal_max_timeout": float64(45_000)}, want: 45_000},
		{name: "zero falls back", values: map[string]any{"background_terminal_max_timeout": 0}, want: DefaultBackgroundTerminalMaxTimeoutMS},
		{name: "negative falls back", values: map[string]any{"background_terminal_max_timeout": -1}, want: DefaultBackgroundTerminalMaxTimeoutMS},
		{name: "wrong type falls back", values: map[string]any{"background_terminal_max_timeout": "fast"}, want: DefaultBackgroundTerminalMaxTimeoutMS},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			cfg := &Config{Values: testCase.values}
			if got := cfg.BackgroundTerminalMaxTimeoutMS(); got != testCase.want {
				t.Fatalf("BackgroundTerminalMaxTimeoutMS() = %d, want %d", got, testCase.want)
			}
		})
	}
	var nilConfig *Config
	if got := nilConfig.BackgroundTerminalMaxTimeoutMS(); got != DefaultBackgroundTerminalMaxTimeoutMS {
		t.Fatalf("nil config = %d, want the default", got)
	}
}
