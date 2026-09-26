package app

import "testing"

// Mirrors Rust's `Tui::default` (`show_tooltips: true`): the configured
// `tui.show_tooltips` value defaults to enabled and an explicit false disables
// the session tip.
func TestInteractiveShowTooltipsDefaultsEnabledLikeRust(t *testing.T) {
	if got := interactiveShowTooltips(nil); got == nil || !*got {
		t.Fatalf("default = %v, want true", got)
	}
	enabled := interactiveShowTooltips(map[string]any{"tui": map[string]any{"show_tooltips": true}})
	if enabled == nil || !*enabled {
		t.Fatalf("configured on = %v, want true", enabled)
	}
	disabled := interactiveShowTooltips(map[string]any{"tui": map[string]any{"show_tooltips": false}})
	if disabled == nil || *disabled {
		t.Fatalf("configured off = %v, want false", disabled)
	}
}
