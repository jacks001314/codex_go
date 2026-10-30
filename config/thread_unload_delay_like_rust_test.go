package config

import (
	"os"
	"strings"
	"testing"
	"time"
)

// Rust #57242 reduces the default idle-thread unload delay to one minute while
// keeping explicit overrides, including zero, authoritative.
func TestThreadUnloadDelayDefaultsToOneMinuteLikeRust(t *testing.T) {
	var nilConfig *Config
	if got := nilConfig.ThreadUnloadDelaySecs(); got != time.Minute {
		t.Fatalf("nil config unload delay = %v, want 30m", got)
	}
	tests := []struct {
		name  string
		value any
		want  time.Duration
	}{
		{name: "absent", want: time.Minute},
		{name: "zero", value: int64(0), want: 0},
		{name: "explicit seconds", value: int64(60), want: time.Minute},
		{name: "non-numeric falls back", value: "120", want: time.Minute},
		{name: "negative falls back", value: int64(-1), want: time.Minute},
		{name: "overflow falls back", value: int64(9223372036854775807), want: time.Minute},
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

// Rust #42320 rejects non-u64 values and deadlines that cannot fit a monotonic
// clock at config load, rather than silently using the default.
func TestThreadUnloadDelayConfigLoadRejectsInvalidValuesLikeRust(t *testing.T) {
	for name, value := range map[string]string{
		"negative":   "-1",
		"fractional": "1.5",
		"string":     `"60"`,
		"overflow":   "9223372036854775807",
	} {
		t.Run(name, func(t *testing.T) {
			home := t.TempDir()
			body := "thread_unload_delay_secs = " + value + "\n"
			if err := os.WriteFile(ConfigPath(home), []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := LoadEffectiveWithOptions(home, nil); err == nil || !strings.Contains(err.Error(), "thread_unload_delay_secs") {
				t.Fatalf("LoadEffectiveWithOptions() error = %v, want a thread_unload_delay_secs rejection", err)
			}
		})
	}
}

func TestThreadUnloadDelayConfigLoadAcceptsZeroLikeRust(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(ConfigPath(home), []byte("thread_unload_delay_secs = 0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadEffectiveWithOptions(home, nil)
	if err != nil {
		t.Fatalf("LoadEffectiveWithOptions() error = %v", err)
	}
	if got := cfg.ThreadUnloadDelaySecs(); got != 0 {
		t.Fatalf("ThreadUnloadDelaySecs() = %v, want zero", got)
	}
}
