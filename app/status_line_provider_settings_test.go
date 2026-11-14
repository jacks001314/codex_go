package app

import "testing"

func TestInteractiveStatusLineProviderFromConfig(t *testing.T) {
	if got := interactiveStatusLineProviderFromConfig(nil); got != nil {
		t.Fatalf("nil config selection = %#v", got)
	}
	if got := interactiveStatusLineProviderFromConfig(map[string]any{"tui": map[string]any{"status_line_provider": "default"}}); got == nil || !got.Default {
		t.Fatalf("default selection = %#v", got)
	}
	got := interactiveStatusLineProviderFromConfig(map[string]any{"tui": map[string]any{
		"status_line_provider": map[string]any{"server": "work", "tool": "usage"},
	}})
	if got == nil || got.Server != "work" || got.Tool != "usage" {
		t.Fatalf("provider selection = %#v", got)
	}
	if got := interactiveStatusLineProviderFromConfig(map[string]any{"tui": map[string]any{"status_line_provider": map[string]any{"server": "work"}}}); got != nil {
		t.Fatalf("incomplete selection = %#v", got)
	}
}
