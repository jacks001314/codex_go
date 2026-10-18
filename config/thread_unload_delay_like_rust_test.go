package config

import (
	"testing"
	"time"
)

// Rust #52081 raises the default idle-thread unload delay to 30 minutes while
// keeping explicit overrides, including zero, authoritative.
func TestThreadUnloadDelayDefaultsToThirtyMinutesLikeRust(t *testing.T) {
	var nilConfig *Config
	if got := nilConfig.ThreadUnloadDelaySecs(); got != 30*time.Minute {
		t.Fatalf("nil config unload delay = %v, want 30m", got)
	}
	tests := []struct {
		name  string
		value any
		want  time.Duration
	}{
		{name: "absent", want: 30 * time.Minute},
		{name: "zero", value: int64(0), want: 0},
		{name: "explicit seconds", value: int64(60), want: time.Minute},
		{name: "non-numeric falls back", value: "120", want: 30 * time.Minute},
		{name: "negative falls back", value: int64(-1), want: 30 * time.Minute},
		{name: "overflow falls back", value: int64(9223372036854775807), want: 30 * time.Minute},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg := &Config{Values: map[string]any{}}
			if test.value != nil {
				cfg.Values["thread_unload_delay_secs"] = test.value
			}
			if got := cfg.ThreadUnloadDelaySecs(); got != test.want {
				t.Fatalf("ThreadUnloadDelaySecs() = %v, want %v", got, test.want)
			}
		})
	}
}
