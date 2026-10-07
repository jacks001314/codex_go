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

// Rust #51510: `LocalSettings::reloaded(&config)` adopts the reloaded
// preferences but restores the fields owned by this launch
// (`transcript_mode`, `tui.alternate_screen`), so a configuration reload can
// never change the running TUI's terminal ownership. Counterpart Rust test:
// `new_thread_keeps_live_settings_after_failed_reload`
// (codex-rs/tui/src/app/config_persistence.rs).
func TestLocalSettingsReloadedRestoresLauncherOwnedFieldsLikeRust(t *testing.T) {
	live := LocalSettings{
		Tui:             SettingsWriteResult{AnimationsEnabled: boolPtrTea(true)},
		AlternateScreen: true,
	}
	reloaded := SettingsWriteResult{
		AnimationsEnabled: boolPtrTea(false),
		TUITheme:          "dracula",
	}

	got := live.Reloaded(reloaded)
	if got.Tui.AnimationsEnabled == nil || *got.Tui.AnimationsEnabled {
		t.Fatalf("reloaded() did not adopt the reloaded preferences: %#v", got.Tui.AnimationsEnabled)
	}
	if got.Tui.TUITheme != "dracula" {
		t.Fatalf("reloaded() dropped the reloaded theme: %q", got.Tui.TUITheme)
	}
	if !got.AlternateScreen {
		t.Fatal("reloaded() overwrote the launcher-owned alternate-screen setting")
	}

	// The Go shape of the same record can also hold the launch's own value.
	got = LocalSettings{Tui: live.Tui}.Reloaded(reloaded)
	if got.AlternateScreen {
		t.Fatal("reloaded() invented an alternate-screen setting")
	}
}

// Rust #51510, counterpart Rust test:
// `new_thread_keeps_live_settings_after_failed_reload`. `load_new_session_config`
// returns `(Config, LocalSettings)`; a failed reload yields
// `(self.config.clone(), self.local_settings.clone())` so the live settings —
// not preferences re-derived from the stale in-memory config — reach the new
// thread.
func TestLoadNewSessionConfigKeepsLiveSettingsLikeRust(t *testing.T) {
	live := LocalSettings{
		Tui: SettingsWriteResult{
			AnimationsEnabled: boolPtrTea(true),
			Rendering:         &RenderingSettings{Math: false},
		},
		AlternateScreen: true,
	}
	// The stale preferences a failed reload would resolve (invalid config.toml).
	stale := SettingsWriteResult{
		AnimationsEnabled: boolPtrTea(false),
		Rendering:         &RenderingSettings{Math: true},
	}

	// A reload that fails keeps the live record, entirely untouched.
	got, err := LoadNewSessionLocalSettings(live, func() (SettingsWriteResult, error) {
		return stale, errors.New("invalid config.toml")
	})
	if err == nil {
		t.Fatal("failed reload must report its error")
	}
	if got.Tui.AnimationsEnabled == nil || !*got.Tui.AnimationsEnabled {
		t.Fatalf("failed reload replaced the live animations setting: %#v", got.Tui.AnimationsEnabled)
	}
	if got.Tui.Rendering == nil || got.Tui.Rendering.Math {
		t.Fatalf("failed reload staged stale rendering preferences: %#v", got.Tui.Rendering)
	}
	if !got.AlternateScreen {
		t.Fatal("failed reload changed the launcher-owned alternate-screen setting")
	}

	// A successful reload adopts its preferences and keeps this launch's screen.
	got, err = LoadNewSessionLocalSettings(live, func() (SettingsWriteResult, error) {
		return stale, nil
	})
	if err != nil {
		t.Fatalf("successful reload reported an error: %v", err)
	}
	if got.Tui.AnimationsEnabled == nil || *got.Tui.AnimationsEnabled {
		t.Fatalf("successful reload did not adopt the reloaded preferences: %#v", got.Tui.AnimationsEnabled)
	}
	if !got.AlternateScreen {
		t.Fatal("successful reload changed the launcher-owned alternate-screen setting")
	}

	// No reload attempted (the host does not rebuild the configuration) keeps the
	// live record and reports no error.
	got, err = LoadNewSessionLocalSettings(live, nil)
	if err != nil {
		t.Fatalf("nil reload must not report an error: %v", err)
	}
	if got.Tui.AnimationsEnabled == nil || !*got.Tui.AnimationsEnabled || !got.AlternateScreen {
		t.Fatalf("nil reload changed the live record: %#v", got)
	}
}

// Rust #51510: `App::confirm_directory_trust` returns
// `Result<Option<LocalSettings>>` — the staged record is replaced only when the
// trust check actually reloaded configuration (`Ok(Some(..))`); a check that did
// not reload (`Ok(None)`) leaves it alone. Counterpart Rust test:
// `new_thread_keeps_live_settings_after_failed_reload`.
func TestConfirmDirectoryTrustOnlyReplacesStagedSettingsLikeRust(t *testing.T) {
	staged := LocalSettings{
		Tui:             SettingsWriteResult{AnimationsEnabled: boolPtrTea(true)},
		AlternateScreen: true,
	}

	// Ok(None): the trust check did not reload configuration.
	got, replaced := LocalSettingsAfterTrustCheck(staged, nil)
	if replaced {
		t.Fatal("a trust check that did not reload configuration replaced the staged settings")
	}
	if got.Tui.AnimationsEnabled == nil || !*got.Tui.AnimationsEnabled || !got.AlternateScreen {
		t.Fatalf("skipped trust check changed the staged settings: %#v", got)
	}

	// Ok(Some(..)): the trust check reloaded configuration.
	reloaded := SettingsWriteResult{AnimationsEnabled: boolPtrTea(false), TUITheme: "dracula"}
	got, replaced = LocalSettingsAfterTrustCheck(staged, &reloaded)
	if !replaced {
		t.Fatal("a reloading trust check must replace the staged settings")
	}
	if got.Tui.AnimationsEnabled == nil || *got.Tui.AnimationsEnabled {
		t.Fatalf("trust check did not stage the reloaded preferences: %#v", got.Tui.AnimationsEnabled)
	}
	if got.Tui.TUITheme != "dracula" {
		t.Fatalf("trust check dropped the reloaded theme: %q", got.Tui.TUITheme)
	}
	if !got.AlternateScreen {
		t.Fatal("trust check changed the launcher-owned alternate-screen setting")
	}
}

// Rust #51510: applying the staged record to the live state merges the
// preferences with the "nil keeps the live value" rule and restores the
// launcher-owned fields from the record, never from the reloaded source.
// Counterpart Rust test: `new_thread_keeps_live_settings_after_failed_reload`.
func TestApplyLocalSettingsKeepsLiveValuesLikeRust(t *testing.T) {
	animations := true
	colors := true
	model := NewModel(codextui.NewState(nil), Options{
		AnimationsEnabled:   &animations,
		StatusLineUseColors: &colors,
		NoAltScreen:         true,
	})
	model.featureSettings = map[string]bool{"foo": true}
	model.tuiTheme = "live-theme"

	// An empty staged record (the state after a failed reload) keeps every live
	// preference and restores this launch's screen ownership.
	model.applyLocalSettings(LocalSettings{AlternateScreen: true})
	if !model.animationsEnabled {
		t.Fatal("empty staged record cleared animationsEnabled")
	}
	if !model.statusLineUseColors {
		t.Fatal("empty staged record cleared statusLineUseColors")
	}
	if !model.featureSettings["foo"] {
		t.Fatalf("empty staged record cleared featureSettings: %#v", model.featureSettings)
	}
	if model.tuiTheme != "live-theme" {
		t.Fatalf("empty staged record cleared tuiTheme: %q", model.tuiTheme)
	}
	if !model.noAltScreen {
		t.Fatal("staged record lost this launch's alternate-screen setting")
	}

	// A partial record only changes the fields it carries.
	model.applyLocalSettings(LocalSettings{
		Tui:             SettingsWriteResult{AnimationsEnabled: boolPtrTea(false), TUITheme: "reloaded-theme"},
		AlternateScreen: true,
	})
	if model.animationsEnabled {
		t.Fatal("partial record did not adopt its animationsEnabled value")
	}
	if !model.statusLineUseColors {
		t.Fatal("partial record cleared a preference it does not carry")
	}
	if model.tuiTheme != "reloaded-theme" {
		t.Fatalf("partial record cleared tuiTheme: %q", model.tuiTheme)
	}
	if model.localSettings.Tui.TUITheme != "reloaded-theme" {
		t.Fatalf("staged record did not adopt the reloaded preferences: %#v", model.localSettings.Tui)
	}
}
