package tea

import (
	"errors"
	"testing"

	codextui "codex_go/tui"
)

// Rust #51510: `LocalSettings::reloaded` / `load_new_session_config` staging.
// Counterpart Rust test: `new_thread_keeps_live_settings_after_failed_reload`
// (codex-rs/tui/src/app/config_persistence.rs).
func TestReloadedLocalSettingsKeepsLiveSettingsLikeRust(t *testing.T) {
	live := SettingsWriteResult{
		FeatureSettings:   map[string]bool{"foo": true},
		UseMemories:       boolPtrTea(true),
		AnimationsEnabled: boolPtrTea(true),
	}
	reloaded := SettingsWriteResult{
		FeatureSettings:   map[string]bool{"foo": false, "bar": true},
		UseMemories:       boolPtrTea(false),
		AnimationsEnabled: boolPtrTea(false),
	}

	// A failed reload keeps the live settings, entirely untouched.
	got := ReloadedLocalSettings(live, reloaded, SettingsReloadFailed)
	if !got.FeatureSettings["foo"] || got.FeatureSettings["bar"] {
		t.Fatalf("failed reload staged stale feature settings: %#v", got.FeatureSettings)
	}
	if got.UseMemories == nil || !*got.UseMemories {
		t.Fatalf("failed reload overwrote live useMemories: %#v", got.UseMemories)
	}
	if got.AnimationsEnabled == nil || !*got.AnimationsEnabled {
		t.Fatalf("failed reload overwrote live animationsEnabled: %#v", got.AnimationsEnabled)
	}

	// No reload attempted (Rust confirm_directory_trust Ok(None)) also keeps it.
	got = ReloadedLocalSettings(live, reloaded, SettingsReloadNone)
	if !got.FeatureSettings["foo"] || got.FeatureSettings["bar"] {
		t.Fatalf("skipped reload staged settings: %#v", got.FeatureSettings)
	}

	// A successful reload adopts the reloaded values.
	got = ReloadedLocalSettings(live, reloaded, SettingsReloadOK)
	if got.FeatureSettings["foo"] || !got.FeatureSettings["bar"] {
		t.Fatalf("successful reload did not adopt settings: %#v", got.FeatureSettings)
	}
	if got.AnimationsEnabled == nil || *got.AnimationsEnabled {
		t.Fatalf("successful reload did not adopt animationsEnabled: %#v", got.AnimationsEnabled)
	}
}

// Rust #51510: the outcome classifier behind `load_new_session_config`'s
// `(Config, LocalSettings)` result and `confirm_directory_trust`'s
// `Result<Option<LocalSettings>>`.
func TestSettingsReloadOutcomeForLikeRust(t *testing.T) {
	result := SettingsWriteResult{Rendering: &RenderingSettings{Mermaid: true}}

	if got := SettingsReloadOutcomeFor(nil, errors.New("invalid config.toml")); got != SettingsReloadFailed {
		t.Fatalf("nil result with error = %v, want SettingsReloadFailed", got)
	}
	if got := SettingsReloadOutcomeFor(nil, nil); got != SettingsReloadNone {
		t.Fatalf("nil result without error = %v, want SettingsReloadNone", got)
	}
	if got := SettingsReloadOutcomeFor(&result, nil); got != SettingsReloadOK {
		t.Fatalf("result without error = %v, want SettingsReloadOK", got)
	}
	if got := SettingsReloadOutcomeFor(&result, errors.New("boom")); got != SettingsReloadFailed {
		t.Fatalf("result with error = %v, want SettingsReloadFailed", got)
	}

	if !ShouldStageReloadedLocalSettings(&result, nil) {
		t.Fatal("successful reload must be stageable")
	}
	if ShouldStageReloadedLocalSettings(&result, errors.New("boom")) {
		t.Fatal("failed reload must not be stageable")
	}
}

// Rust #51510, counterpart Rust test: `new_thread_keeps_live_settings_after_failed_reload`.
// The test changes a live settings value, makes the configuration reload fail,
// and verifies the live settings survive. Scope note: in Go the configuration
// reload entry point lives in the app layer (app/interactive.go's
// interactiveLoadSettings / interactiveSettingsWriteHandler, outside the
// tui/tea write scope of this change), so this test drives the equivalent
// tui/tea surface: a failed settings/reload result pushed through Model.Update
// must not touch the live Model settings, and the same result classified via
// SettingsReloadOutcomeFor/ReloadedLocalSettings must keep the live settings.
func TestNewThreadKeepsLiveSettingsAfterFailedReloadLikeRust(t *testing.T) {
	animations := true
	colors := true
	tooltips := true
	model := NewModel(codextui.NewState(nil), Options{
		FeatureSettings:     map[string]bool{"foo": true},
		AnimationsEnabled:   &animations,
		StatusLineUseColors: &colors,
		ShowTooltips:        &tooltips,
		NoAltScreen:         true,
	})
	model.featureSettings["foo"] = true

	live := SettingsWriteResult{
		FeatureSettings:   cloneBoolMapTea(model.featureSettings),
		AnimationsEnabled: boolPtrTea(model.animationsEnabled),
		Rendering: &RenderingSettings{
			Mermaid: true,
			Math:    true,
			Tables:  true,
			Lists:   true,
		},
	}

	// The stale result a failed reload would produce (invalid config.toml).
	reloaded := SettingsWriteResult{
		FeatureSettings:   map[string]bool{"foo": false},
		AnimationsEnabled: boolPtrTea(false),
		Rendering:         &RenderingSettings{Mermaid: false, Math: false, Tables: false, Lists: false},
	}

	// A failed configuration reload must keep the live local settings...
	if got := ReloadedLocalSettings(live, reloaded, SettingsReloadOutcomeFor(&reloaded, errors.New("invalid config.toml"))); got.AnimationsEnabled == nil || !*got.AnimationsEnabled {
		t.Fatalf("failed reload replaced the live animations setting: %#v", got.AnimationsEnabled)
	}

	// ...and the same holds when it is delivered over the settings-write path.
	model.pendingSettingsRequestID = 11
	model.Update(SettingsWriteResultMsg{
		RequestID: 11,
		Kind:      "reload",
		Result:    reloaded,
		Err:       errors.New("invalid config.toml"),
	})

	if !model.featureSettings["foo"] {
		t.Fatalf("failed reload overwrote live featureSettings: %#v", model.featureSettings)
	}
	if !model.animationsEnabled {
		t.Fatal("failed reload overwrote live animationsEnabled")
	}
	if !model.statusLineUseColors {
		t.Fatal("failed reload overwrote live statusLineUseColors")
	}
	if !model.showTooltips {
		t.Fatal("failed reload overwrote live showTooltips")
	}
	// Rust restores launcher-owned fields (`tui.alternate_screen`) from
	// LocalSettings; in Go the corresponding Model.noAltScreen must stay put.
	if !model.noAltScreen {
		t.Fatal("failed reload changed the launcher-owned noAltScreen setting")
	}
}

func boolPtrTea(value bool) *bool {
	return &value
}
