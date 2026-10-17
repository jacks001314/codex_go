package config

import "fmt"

const (
	// DefaultAsyncCompactThresholdPercent is Rust's
	// `model_async_compact_threshold_percent` default (#53595).
	DefaultAsyncCompactThresholdPercent = 65
	MinAsyncCompactThresholdPercent     = 1
	MaxAsyncCompactThresholdPercent     = 100
)

// AsyncCompactionConfig mirrors Rust `AsyncCompactionConfigToml` (#53595):
// `features.async_compaction` accepts either a boolean (the feature toggle) or a
// table with `enabled` and `model_async_compact_threshold_percent`. The
// threshold defaults to 65, is valid in 1..100, and is validated even when the
// feature is disabled; setting it alone does not enable the feature.
type AsyncCompactionConfig struct {
	Enabled                           bool
	ModelAsyncCompactThresholdPercent int
}

// AsyncCompactionConfig resolves the effective async-compaction settings.
func (c *Config) AsyncCompactionConfig() (*AsyncCompactionConfig, error) {
	out := &AsyncCompactionConfig{ModelAsyncCompactThresholdPercent: DefaultAsyncCompactThresholdPercent}
	if c == nil || c.Values == nil {
		return out, nil
	}
	features, _ := c.Values["features"].(map[string]any)
	raw, ok := features["async_compaction"]
	if !ok {
		return out, nil
	}
	switch value := raw.(type) {
	case bool:
		out.Enabled = value
		return out, nil
	case map[string]any:
		if enabled, ok := value["enabled"].(bool); ok {
			out.Enabled = enabled
		}
		percentRaw, ok := value["model_async_compact_threshold_percent"]
		if !ok {
			return out, nil
		}
		percent, ok := asyncCompactPercentFromConfigValue(percentRaw)
		if !ok {
			return nil, fmt.Errorf("features.async_compaction.model_async_compact_threshold_percent must be an integer between %d and %d", MinAsyncCompactThresholdPercent, MaxAsyncCompactThresholdPercent)
		}
		if percent < MinAsyncCompactThresholdPercent || percent > MaxAsyncCompactThresholdPercent {
			return nil, fmt.Errorf("features.async_compaction.model_async_compact_threshold_percent must be between %d and %d", MinAsyncCompactThresholdPercent, MaxAsyncCompactThresholdPercent)
		}
		out.ModelAsyncCompactThresholdPercent = percent
		return out, nil
	default:
		return nil, fmt.Errorf("features.async_compaction must be a boolean or a table")
	}
}

// asyncCompactPercentFromConfigValue accepts the numeric shapes the config
// loader produces, rejecting fractions and non-positive-machine values.
func asyncCompactPercentFromConfigValue(value any) (int, bool) {
	switch v := value.(type) {
	case int:
		return v, true
	case int64:
		return int(v), true
	case uint64:
		if v > uint64(^uint(0)>>1) {
			return 0, false
		}
		return int(v), true
	case float64:
		if v != float64(int64(v)) {
			return 0, false
		}
		return int(v), true
	default:
		return 0, false
	}
}
