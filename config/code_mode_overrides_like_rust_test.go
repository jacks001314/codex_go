package config

import (
	"reflect"
	"strings"
	"testing"
)

// TestCodeModeOverridesPreserveEnabledStateAndCellOverheadLikeRust mirrors Rust
// config/src/merge_tests.rs (#46288): adding the experimental cell-overhead
// display option must keep a legacy `code_mode` boolean toggle, in either
// override order and in either merge direction, for the top-level feature table
// and a profile-scoped one.
func TestCodeModeOverridesPreserveEnabledStateAndCellOverheadLikeRust(t *testing.T) {
	for _, featurePath := range []string{"features", "profiles.work.features"} {
		for _, enabled := range []bool{true, false} {
			expected := codeModeLayer(featurePath, map[string]any{
				"enabled":                         enabled,
				"experimental_show_cell_overhead": true,
			})
			toggle := Override{Path: featurePath + ".code_mode", Value: enabled}
			overhead := Override{Path: featurePath + ".code_mode.experimental_show_cell_overhead", Value: true}
			for _, overrides := range [][]Override{{toggle, overhead}, {overhead, toggle}} {
				if got := BuildCLIOverridesLayer(overrides); !reflect.DeepEqual(got, expected) {
					t.Fatalf("%s code_mode=%v overrides = %#v, want %#v", featurePath, enabled, got, expected)
				}
			}

			merged := [][2]map[string]any{
				{codeModeLayer(featurePath, enabled), codeModeLayer(featurePath, map[string]any{"experimental_show_cell_overhead": true})},
				{codeModeLayer(featurePath, map[string]any{"experimental_show_cell_overhead": true}), codeModeLayer(featurePath, enabled)},
			}
			for _, layers := range merged {
				base, overlay := layers[0], layers[1]
				mergeConfigMaps(base, overlay)
				if !reflect.DeepEqual(base, expected) {
					t.Fatalf("%s code_mode=%v merged = %#v, want %#v", featurePath, enabled, base, expected)
				}
				if featurePath != "features" {
					continue
				}
				// Profile-scoped features reach the runtime only after the
				// profile is applied, so the resolved view is checked on the
				// top-level table.
				resolved := &Config{Values: base}
				if got := resolved.FeatureSettings()["code_mode"]; got != enabled {
					t.Fatalf("%s code_mode=%v resolved feature = %v, want %v", featurePath, enabled, got, enabled)
				}
				if !resolved.CodeModeExperimentalShowCellOverhead() {
					t.Fatalf("%s code_mode=%v lost experimental_show_cell_overhead", featurePath, enabled)
				}
			}
		}
	}
}

// codeModeLayer builds the nested config document for `<featurePath>.code_mode`
// holding value.
func codeModeLayer(featurePath string, value any) map[string]any {
	segments := append(strings.Split(featurePath, "."), "code_mode")
	var current any = value
	for i := len(segments) - 1; i >= 0; i-- {
		current = map[string]any{segments[i]: current}
	}
	return current.(map[string]any)
}
