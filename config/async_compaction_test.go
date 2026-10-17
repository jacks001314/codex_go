package config

import (
	"strings"
	"testing"
)

// TestAsyncCompactionConfigLikeRust mirrors Rust #53595: the feature accepts a
// boolean or a table, defaults the threshold to 65, validates 1..100 even when
// disabled, and a threshold alone does not enable the feature.
func TestAsyncCompactionConfigLikeRust(t *testing.T) {
	for _, testCase := range []struct {
		name        string
		value       any
		wantEnabled bool
		wantPercent int
	}{
		{name: "absent", wantPercent: 65},
		{name: "boolean true", value: true, wantEnabled: true, wantPercent: 65},
		{name: "boolean false", value: false, wantPercent: 65},
		{name: "table default", value: map[string]any{}, wantPercent: 65},
		{name: "table enabled", value: map[string]any{"enabled": true}, wantEnabled: true, wantPercent: 65},
		{name: "threshold alone does not enable", value: map[string]any{"model_async_compact_threshold_percent": int64(80)}, wantPercent: 80},
		{name: "custom threshold and enabled", value: map[string]any{"enabled": true, "model_async_compact_threshold_percent": 1}, wantEnabled: true, wantPercent: 1},
		{name: "upper bound", value: map[string]any{"model_async_compact_threshold_percent": 100}, wantPercent: 100},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			cfg := &Config{Values: map[string]any{}}
			if testCase.name != "absent" {
				cfg.Values["features"] = map[string]any{"async_compaction": testCase.value}
			}
			resolved, err := cfg.AsyncCompactionConfig()
			if err != nil {
				t.Fatalf("AsyncCompactionConfig() error = %v", err)
			}
			if resolved.Enabled != testCase.wantEnabled || resolved.ModelAsyncCompactThresholdPercent != testCase.wantPercent {
				t.Fatalf("config = %#v, want enabled=%v percent=%d", resolved, testCase.wantEnabled, testCase.wantPercent)
			}
		})
	}
}

// TestAsyncCompactionConfigRejectsInvalidValuesLikeRust pins the upstream
// validation: out-of-range or fractional thresholds are rejected even when the
// feature is disabled, and a non-boolean/non-table value is rejected.
func TestAsyncCompactionConfigRejectsInvalidValuesLikeRust(t *testing.T) {
	for _, testCase := range []struct {
		name  string
		value any
		want  string
	}{
		{name: "zero while disabled", value: map[string]any{"model_async_compact_threshold_percent": 0}, want: "must be between 1 and 100"},
		{name: "above range", value: map[string]any{"enabled": false, "model_async_compact_threshold_percent": 101}, want: "must be between 1 and 100"},
		{name: "fractional", value: map[string]any{"model_async_compact_threshold_percent": 65.5}, want: "must be an integer"},
		{name: "string", value: "enabled", want: "must be a boolean or a table"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			cfg := &Config{Values: map[string]any{"features": map[string]any{"async_compaction": testCase.value}}}
			_, err := cfg.AsyncCompactionConfig()
			if err == nil || !strings.Contains(err.Error(), testCase.want) {
				t.Fatalf("AsyncCompactionConfig() error = %v, want %q", err, testCase.want)
			}
		})
	}
}

// TestAsyncCompactionUnknownFieldRejectedLikeRust pins serde deny_unknown_fields
// for the new table.
func TestAsyncCompactionUnknownFieldRejectedLikeRust(t *testing.T) {
	err := validateKnownFeatureFields(map[string]any{
		"async_compaction": map[string]any{"unknown_setting": true},
	})
	if err == nil || !strings.Contains(err.Error(), "unknown configuration field `features.async_compaction.unknown_setting`") {
		t.Fatalf("ValidateValues() error = %v", err)
	}
}
