package app

import (
	"testing"

	"codex_go/config"
)

func TestInteractiveSettingsLoadsAndClearsTUIPresetsLikeRust(t *testing.T) {
	settings := interactiveSettingsFromConfig(&config.Config{
		Values: map[string]any{
			"tui": map[string]any{
				"presets": map[string]any{
					"1": map[string]any{"input": "hello from config"},
				},
			},
		},
	})
	if preset, ok := settings.QuickDials["1"]; !ok || preset.Input != "hello from config" {
		t.Fatalf("loaded presets = %#v", settings.QuickDials)
	}

	// A reload with the table removed must produce a non-nil empty map so the
	// TUI clears the deleted preset instead of preserving the live value.
	settings = interactiveSettingsFromConfig(&config.Config{Values: map[string]any{}})
	if settings.QuickDials == nil || len(settings.QuickDials) != 0 {
		t.Fatalf("missing presets normalized to %#v, want an empty set", settings.QuickDials)
	}
}
