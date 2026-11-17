package config

import (
	"os"
	"strings"
	"testing"
)

// Rust parity: codex-rs/config/src/config_toml.rs RolloutTraceToml
// (`[rollout_trace]` capture limits, 59ac39addb). Strict config must accept the
// table Rust accepts and reject keys or values Rust rejects.
func TestStrictConfigAcceptsRolloutTraceLimitsLikeRust(t *testing.T) {
	found := false
	for _, key := range KnownTopLevelConfigFields() {
		if key == "rollout_trace" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("KnownTopLevelConfigFields is missing rollout_trace")
	}

	dir := t.TempDir()
	body := "[rollout_trace]\nmax_payload_bytes = 64\nmax_bytes = 4096\n"
	if err := os.WriteFile(ConfigPath(dir), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadEffectiveWithOptions(dir, &EffectiveOptions{StrictConfig: true})
	if err != nil {
		t.Fatalf("strict load returned error: %v", err)
	}
	if _, ok := cfg.Values["rollout_trace"].(map[string]any); !ok {
		t.Fatalf("rollout_trace not carried: %#v", cfg.Values["rollout_trace"])
	}

	unknownDir := t.TempDir()
	if err := os.WriteFile(ConfigPath(unknownDir), []byte("[rollout_trace]\nbogus = 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = LoadEffectiveWithOptions(unknownDir, &EffectiveOptions{StrictConfig: true})
	if err == nil || !strings.Contains(err.Error(), "unknown configuration field `rollout_trace.bogus`") {
		t.Fatalf("strict load error = %v, want the unknown rollout_trace key", err)
	}

	for _, testCase := range []struct {
		name   string
		values map[string]any
		want   string
	}{
		{
			name:   "negative payload cap",
			values: map[string]any{"rollout_trace": map[string]any{"max_payload_bytes": int64(-1)}},
			want:   "rollout_trace.max_payload_bytes",
		},
		{
			name:   "string bundle cap",
			values: map[string]any{"rollout_trace": map[string]any{"max_bytes": "big"}},
			want:   "rollout_trace.max_bytes",
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			err := ValidateConfigValues(testCase.values)
			if err == nil || !strings.Contains(err.Error(), testCase.want) {
				t.Fatalf("ValidateConfigValues error = %v, want %q", err, testCase.want)
			}
		})
	}
}
