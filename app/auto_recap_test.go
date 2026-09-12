package app

import "testing"

// TestInteractiveAutoRecapHonorsConfig pins Rust's `tui.auto_recap` config
// (local_settings.rs): the default is enabled, and an explicit boolean wins.
func TestInteractiveAutoRecapHonorsConfig(t *testing.T) {
	if got := interactiveAutoRecap(nil); got == nil || !*got {
		t.Fatalf("default auto_recap = %v, want enabled", got)
	}
	if got := interactiveAutoRecap(map[string]any{"tui": map[string]any{"auto_recap": false}}); got == nil || *got {
		t.Fatalf("auto_recap=false = %v, want disabled", got)
	}
	if got := interactiveAutoRecap(map[string]any{"tui": map[string]any{"auto_recap": true}}); got == nil || !*got {
		t.Fatalf("auto_recap=true = %v, want enabled", got)
	}
	if got := interactiveAutoRecap(map[string]any{"tui": map[string]any{"auto_recap": "nope"}}); got == nil || !*got {
		t.Fatalf("invalid auto_recap = %v, want the enabled default", got)
	}
}
