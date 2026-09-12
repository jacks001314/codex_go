package app

import "testing"

func TestInteractiveStatusLineUseColorsHonorsConfig(t *testing.T) {
	if got := interactiveStatusLineUseColors(nil); got == nil || !*got {
		t.Fatalf("default status_line_use_colors = %#v, want enabled", got)
	}
	if got := interactiveStatusLineUseColors(map[string]any{"tui": map[string]any{"status_line_use_colors": false}}); got == nil || *got {
		t.Fatalf("status_line_use_colors=false = %#v, want disabled", got)
	}
	if got := interactiveStatusLineUseColors(map[string]any{"tui": map[string]any{"status_line_use_colors": true}}); got == nil || !*got {
		t.Fatalf("status_line_use_colors=true = %#v, want enabled", got)
	}
}
