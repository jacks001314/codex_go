package config

import "testing"

// Mirrors Rust's `TuiEffects` defaults and `#[serde(default)]` behavior.
func TestTuiEffectsFromValuesLikeRust(t *testing.T) {
	for _, values := range []map[string]any{
		nil,
		{},
		{"tui": map[string]any{}},
		{"tui": map[string]any{"effects": map[string]any{}}},
		{"tui": map[string]any{"effects": map[string]any{"welcome": "yes"}}},
	} {
		if got := TuiEffectsFromValues(values); got != DefaultTuiEffects() {
			t.Fatalf("TuiEffectsFromValues(%#v) = %#v, want the default", values, got)
		}
	}

	got := TuiEffectsFromValues(map[string]any{
		"tui": map[string]any{"effects": map[string]any{"welcome": false, "progress": false}},
	})
	want := TuiEffects{Starfield: true, Shimmer: true, Welcome: false, Effort: true, Progress: false, Title: true}
	if got != want {
		t.Fatalf("override effects = %#v, want %#v", got, want)
	}

	// Unknown members are ignored, matching serde without deny_unknown_fields.
	got = TuiEffectsFromValues(map[string]any{
		"tui": map[string]any{"effects": map[string]any{"title": false, "future": true}},
	})
	want = TuiEffects{Starfield: true, Shimmer: true, Welcome: true, Effort: true, Progress: true, Title: false}
	if got != want {
		t.Fatalf("unknown member effects = %#v, want %#v", got, want)
	}
}

func TestConfigTuiEffects(t *testing.T) {
	if got := (*Config)(nil).TuiEffects(); got != DefaultTuiEffects() {
		t.Fatalf("nil config effects = %#v, want the default", got)
	}
	cfg := &Config{Values: map[string]any{"tui": map[string]any{"effects": map[string]any{"shimmer": false}}}}
	want := TuiEffects{Starfield: true, Shimmer: false, Welcome: true, Effort: true, Progress: true, Title: true}
	if got := cfg.TuiEffects(); got != want {
		t.Fatalf("config effects = %#v, want %#v", got, want)
	}
}
