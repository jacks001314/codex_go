package config

import (
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
)

// TestSleepToolOverridesPreserveDisabledStateAndModeLikeRust mirrors Rust
// config/src/merge_tests.rs (#41243): a legacy `sleep_tool` boolean override and
// the nested `mode` override must combine into an `enabled`/`mode` table in
// either override order and in either merge direction, for the top-level
// feature table and a profile-scoped one.
func TestSleepToolOverridesPreserveDisabledStateAndModeLikeRust(t *testing.T) {
	for _, featurePath := range []string{"features", "profiles.work.features"} {
		expected := sleepToolLayer(featurePath, map[string]any{"enabled": false, "mode": "always_on"})
		toggle := Override{Path: featurePath + ".sleep_tool", Value: false}
		mode := Override{Path: featurePath + ".sleep_tool.mode", Value: "always_on"}
		for _, overrides := range [][]Override{{toggle, mode}, {mode, toggle}} {
			if got := BuildCLIOverridesLayer(overrides); !reflect.DeepEqual(got, expected) {
				t.Fatalf("%s sleep_tool overrides = %#v, want %#v", featurePath, got, expected)
			}
		}

		merged := [][2]map[string]any{
			{sleepToolLayer(featurePath, false), sleepToolLayer(featurePath, map[string]any{"mode": "always_on"})},
			{sleepToolLayer(featurePath, map[string]any{"mode": "always_on"}), sleepToolLayer(featurePath, false)},
		}
		for _, layers := range merged {
			base, overlay := layers[0], layers[1]
			mergeConfigMaps(base, overlay)
			if !reflect.DeepEqual(base, expected) {
				t.Fatalf("%s merged = %#v, want %#v", featurePath, base, expected)
			}
		}
	}

	resolved := &Config{Values: expectedTopLevelSleepToolLayer()}
	if settings := resolved.FeatureSettings(); settings["sleep_tool"] {
		t.Fatalf("sleep_tool = true, want the structured toggle to disable it")
	}
	if got := resolved.SleepToolMode(); got != SleepToolModeAlwaysOn {
		t.Fatalf("SleepToolMode = %q, want always_on despite the disable toggle", got)
	}
}

func expectedTopLevelSleepToolLayer() map[string]any {
	return map[string]any{"features": map[string]any{"sleep_tool": map[string]any{"enabled": false, "mode": "always_on"}}}
}

// sleepToolLayer builds the nested config document for
// `<featurePath>.sleep_tool` holding value.
func sleepToolLayer(featurePath string, value any) map[string]any {
	segments := append(strings.Split(featurePath, "."), "sleep_tool")
	var current any = value
	for i := len(segments) - 1; i >= 0; i-- {
		current = map[string]any{segments[i]: current}
	}
	return current.(map[string]any)
}

// TestSleepToolFeatureTogglePreservesModeLikeRust mirrors Rust
// core/src/config/edit_tests.rs (#41243): `codex features enable/disable
// sleep_tool` must write `enabled` into the existing `[features.sleep_tool]`
// table instead of adding a conflicting boolean key at the `[features]` level.
func TestSleepToolFeatureTogglePreservesModeLikeRust(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(ConfigPath(home), []byte("[features.sleep_tool]\nmode = \"always_on\"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile config returned error: %v", err)
	}
	for _, enabled := range []bool{false, true} {
		if err := SetFeature(home, "sleep_tool", enabled); err != nil {
			t.Fatalf("SetFeature(%t) returned error: %v", enabled, err)
		}
		cfg, err := Load(home)
		if err != nil {
			t.Fatalf("Load after toggle %t returned error: %v", enabled, err)
		}
		if got := cfg.FeatureSettings()["sleep_tool"]; got != enabled {
			t.Fatalf("sleep_tool after toggle = %v, want %v", got, enabled)
		}
		if got := cfg.SleepToolMode(); got != SleepToolModeAlwaysOn {
			t.Fatalf("SleepToolMode after toggle = %q, want always_on", got)
		}
	}
}

// TestSleepToolFeatureTogglePreservesInlineModeLikeRust covers the inline-table
// spelling of the same structured feature config.
func TestSleepToolFeatureTogglePreservesInlineModeLikeRust(t *testing.T) {
	home := t.TempDir()
	body := "[features]\nsleep_tool = { mode = \"always_on\" }\n"
	if err := os.WriteFile(ConfigPath(home), []byte(body), 0o600); err != nil {
		t.Fatalf("WriteFile config returned error: %v", err)
	}
	if err := SetFeature(home, "sleep_tool", false); err != nil {
		t.Fatalf("SetFeature returned error: %v", err)
	}
	data, err := os.ReadFile(ConfigPath(home))
	if err != nil {
		t.Fatalf("ReadFile returned error: %v", err)
	}
	if !strings.Contains(string(data), `sleep_tool = { enabled = false, mode = "always_on" }`) {
		t.Fatalf("inline sleep_tool config lost its mode: %s", string(data))
	}
	cfg, err := Load(home)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if cfg.FeatureSettings()["sleep_tool"] {
		t.Fatalf("sleep_tool = true, want false")
	}
	if got := cfg.SleepToolMode(); got != SleepToolModeAlwaysOn {
		t.Fatalf("SleepToolMode = %q, want always_on", got)
	}
}

// TestSleepToolConfigRejectsUnknownModeLikeRust mirrors Rust
// features/src/tests.rs (#41243): SleepToolMode is a closed enum, so an
// unrecognized mode fails config load instead of falling back to model_driven.
func TestSleepToolConfigRejectsUnknownModeLikeRust(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
		want string
	}{
		{
			name: "top level",
			body: "[features.sleep_tool]\nmode = \"off\"\n",
			want: "features.sleep_tool.mode",
		},
		{
			name: "profile",
			body: "[profiles.work.features.sleep_tool]\nmode = \"off\"\n",
			want: "profiles.work.features.sleep_tool.mode",
		},
		{
			name: "non string",
			body: "[features.sleep_tool]\nmode = 3\n",
			want: "features.sleep_tool.mode",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			if err := os.WriteFile(ConfigPath(home), []byte(tc.body), 0o600); err != nil {
				t.Fatalf("WriteFile config returned error: %v", err)
			}
			_, err := LoadEffective(home, nil, nil, nil)
			if err == nil {
				t.Fatalf("LoadEffective accepted an unknown sleep_tool.mode")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("LoadEffective error = %v, want mention of %s", err, tc.want)
			}
		})
	}

	for _, valid := range []string{"model_driven", "always_on"} {
		home := t.TempDir()
		body := fmt.Sprintf("[features.sleep_tool]\nmode = %q\n", valid)
		if err := os.WriteFile(ConfigPath(home), []byte(body), 0o600); err != nil {
			t.Fatalf("WriteFile config returned error: %v", err)
		}
		if _, err := LoadEffective(home, nil, nil, nil); err != nil {
			t.Fatalf("LoadEffective(%q) returned error: %v", valid, err)
		}
	}
}

// TestStrictConfigRejectsUnknownSleepToolFieldLikeRust covers
// SleepToolConfigToml's deny_unknown_fields.
func TestStrictConfigRejectsUnknownSleepToolFieldLikeRust(t *testing.T) {
	err := validateKnownTopLevelConfigFields(map[string]any{
		"features": map[string]any{"sleep_tool": map[string]any{"bogus": true}},
	})
	if err == nil || !strings.Contains(err.Error(), "features.sleep_tool.bogus") {
		t.Fatalf("validateKnownTopLevelConfigFields error = %v, want features.sleep_tool.bogus", err)
	}
}

// TestConfigEditStructuredFeatureTogglePreservesNestedConfigLikeRust covers
// the config value RPC path (core/src/config/edit.rs ConfigDocument::apply).
func TestConfigEditStructuredFeatureTogglePreservesNestedConfigLikeRust(t *testing.T) {
	root := map[string]any{"features": map[string]any{"sleep_tool": map[string]any{"mode": "always_on"}}}
	applyEdit(root, &ConfigEdit{KeyPath: "features.sleep_tool", Value: false})
	want := map[string]any{"features": map[string]any{"sleep_tool": map[string]any{"enabled": false, "mode": "always_on"}}}
	if !reflect.DeepEqual(root, want) {
		t.Fatalf("applyEdit result = %#v, want %#v", root, want)
	}

	// Without a structured table the legacy boolean is written as-is, matching
	// the default feature write path.
	root = map[string]any{}
	applyEdit(root, &ConfigEdit{KeyPath: "features.sleep_tool", Value: false})
	want = map[string]any{"features": map[string]any{"sleep_tool": false}}
	if !reflect.DeepEqual(root, want) {
		t.Fatalf("applyEdit default result = %#v, want %#v", root, want)
	}
}
