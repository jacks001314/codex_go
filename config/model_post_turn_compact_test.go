
package config

import (
	"os"
	"strings"
	"testing"
)

// Mirrors Rust #46541: model_post_turn_compact_threshold_percent is a percentage
// in 0..=100, omitted or zero disables turn-end compaction, and a value above
// 100 fails the config load.
func TestModelPostTurnCompactThresholdPercentLikeRust(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(ConfigPath(home), []byte("model_post_turn_compact_threshold_percent = 250\n"), 0o600); err != nil {
		t.Fatalf("write config error = %v", err)
	}
	_, err := LoadEffectiveWithOptions(home, nil)
	if err == nil || !strings.Contains(err.Error(), "model_post_turn_compact_threshold_percent must be between 0 and 100") {
		t.Fatalf("LoadEffectiveWithOptions() error = %v, want the range rejection", err)
	}

	if err := os.WriteFile(ConfigPath(home), []byte("model_post_turn_compact_threshold_percent = 40\n"), 0o600); err != nil {
		t.Fatalf("write config error = %v", err)
	}
	cfg, err := LoadEffectiveWithOptions(home, nil)
	if err != nil {
		t.Fatalf("LoadEffectiveWithOptions() error = %v", err)
	}
	if got := cfg.ModelPostTurnCompactThresholdPercent(); got != 40 {
		t.Fatalf("threshold = %d, want 40", got)
	}

	// Omitted and explicit zero both disable turn-end compaction.
	if got := (&Config{}).ModelPostTurnCompactThresholdPercent(); got != 0 {
		t.Fatalf("unset threshold = %d, want 0", got)
	}
	zero := &Config{Values: map[string]any{"model_post_turn_compact_threshold_percent": int64(0)}}
	if got := zero.ModelPostTurnCompactThresholdPercent(); got != 0 {
		t.Fatalf("zero threshold = %d, want 0", got)
	}
}
