package config

import "testing"

// Mirrors Rust's `TuiRendering` defaults and `#[serde(default)]` behavior: an
// absent table or member enables every renderer, an explicit member overrides
// only itself, and a malformed member falls back rather than failing the load.
func TestTuiRenderingFromValuesLikeRust(t *testing.T) {
	for _, values := range []map[string]any{
		nil,
		{},
		{"tui": map[string]any{}},
		{"tui": map[string]any{"rendering": map[string]any{}}},
		{"tui": map[string]any{"rendering": map[string]any{"math": "yes"}}},
	} {
		if got := TuiRenderingFromValues(values); got != DefaultTuiRendering() {
			t.Fatalf("TuiRenderingFromValues(%#v) = %#v, want the default", values, got)
		}
	}

	got := TuiRenderingFromValues(map[string]any{
		"tui": map[string]any{"rendering": map[string]any{"mermaid": false, "tables": false}},
	})
	want := TuiRendering{Mermaid: false, Math: true, Tables: false, Lists: true}
	if got != want {
		t.Fatalf("override rendering = %#v, want %#v", got, want)
	}

	// Unknown members are ignored, matching serde without deny_unknown_fields.
	got = TuiRenderingFromValues(map[string]any{
		"tui": map[string]any{"rendering": map[string]any{"lists": false, "future": true}},
	})
	want = TuiRendering{Mermaid: true, Math: true, Tables: true, Lists: false}
	if got != want {
		t.Fatalf("unknown member rendering = %#v, want %#v", got, want)
	}
}

// The loaded configuration exposes the same preferences.
func TestConfigTuiRendering(t *testing.T) {
	if got := (*Config)(nil).TuiRendering(); got != DefaultTuiRendering() {
		t.Fatalf("nil config rendering = %#v, want the default", got)
	}
	cfg := &Config{Values: map[string]any{"tui": map[string]any{"rendering": map[string]any{"mermaid": false}}}}
	want := TuiRendering{Mermaid: false, Math: true, Tables: true, Lists: true}
	if got := cfg.TuiRendering(); got != want {
		t.Fatalf("config rendering = %#v, want %#v", got, want)
	}
}
