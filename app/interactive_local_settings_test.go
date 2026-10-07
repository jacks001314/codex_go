package app

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"codex_go/cli"
	codextea "codex_go/tui/tea"
)

// localSettingsBoolPtr keeps this file self-contained (app tests already define
// stringPtr in remote_tui_managed_defaults_test.go).
func localSettingsBoolPtr(value bool) *bool { return &value }

func localSettingsWriteConfig(t *testing.T, home string, body string) string {
	t.Helper()
	path := filepath.Join(home, "config.toml")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}

// Rust #51510, counterpart Rust test:
// `new_thread_keeps_live_settings_after_failed_reload`
// (codex-rs/tui/src/app/config_persistence.rs). The Rust test installs a live
// rendering preference, writes an invalid config.toml, and asserts
// `load_new_session_config` hands back the live `LocalSettings`
// (`assert_eq!(settings, app.local_settings)`). The Go reload entry
// (app/interactive.go interactiveReloadLocalSettings) must behave the same way.
func TestInteractiveReloadLocalSettingsKeepsLiveSettingsLikeRust(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	localSettingsWriteConfig(t, home, "[tui]\nshow_tooltips = true\n")

	// A live setting can be newer than the last successful disk load.
	liveTui := codextea.SettingsWriteResult{
		FeatureSettings:   map[string]bool{"foo": true},
		AnimationsEnabled: localSettingsBoolPtr(true),
		ShowTooltips:      localSettingsBoolPtr(true),
		Rendering: &codextea.RenderingSettings{
			Mermaid: true,
			Math:    false,
			Tables:  true,
			Lists:   true,
		},
	}
	root := &cli.RootOptions{}
	live := interactiveLocalSettingsFromLive(root, liveTui)
	if live.NoAltScreen {
		t.Fatal("launch record must carry the launcher's terminal flag (NoAltScreen=false)")
	}

	// Success path (Rust `load_new_session_config` taking
	// `self.local_settings.reloaded(&config)`): the reloaded preferences are
	// adopted, while the launcher-owned terminal flag is restored from the launch.
	localSettingsWriteConfig(t, home, "[tui]\nshow_tooltips = false\n")
	adopted, err := interactiveReloadLocalSettings(root, live)
	if err != nil {
		t.Fatalf("reload with a valid config returned an error: %v", err)
	}
	if adopted.Tui.ShowTooltips == nil || *adopted.Tui.ShowTooltips {
		t.Fatalf("successful reload did not adopt the reloaded preference: %#v", adopted.Tui.ShowTooltips)
	}
	if adopted.NoAltScreen != live.NoAltScreen {
		t.Fatal("successful reload changed the launcher-owned terminal flag")
	}

	// The directory can regress to an invalid config.toml after a live change.
	localSettingsWriteConfig(t, home, "[broken")
	failed, err := interactiveReloadLocalSettings(root, live)
	if err == nil {
		t.Fatal("expected the reload of an invalid config.toml to fail")
	}
	if !reflect.DeepEqual(failed.Tui, liveTui) {
		t.Fatalf("failed reload replaced the live settings:\ngot  %#v\nwant %#v", failed.Tui, liveTui)
	}
	if failed.NoAltScreen != live.NoAltScreen {
		t.Fatal("failed reload changed the launcher-owned terminal flag")
	}
}

// Rust #51510, counterpart Rust test: `confirm_directory_trust`'s
// `Result<Option<LocalSettings>>` contract, exercised by
// `resume_config.rs` (`if let Some(local_settings) = ... { resume_config.1 = local_settings }`)
// and `agents_overview.rs`. Only a trust check that actually reloaded
// configuration may replace the staged record.
func TestInteractiveLocalSettingsAfterTrustCheckLikeRust(t *testing.T) {
	staged := interactiveLocalSettings{
		Tui: codextea.SettingsWriteResult{
			FeatureSettings:   map[string]bool{"foo": true},
			AnimationsEnabled: localSettingsBoolPtr(true),
			ShowTooltips:      localSettingsBoolPtr(true),
		},
		NoAltScreen: true,
	}

	// A check that did not reload configuration (Rust `Ok(None)`) leaves the
	// staged record untouched.
	kept, replaced := interactiveLocalSettingsAfterTrustCheck(staged, nil)
	if replaced {
		t.Fatal("a trust check that did not reload config must not replace the staged record")
	}
	if !reflect.DeepEqual(kept, staged) {
		t.Fatalf("staged record changed without a reload:\ngot  %#v\nwant %#v", kept, staged)
	}

	// A check that reloaded configuration (Rust `Ok(Some(local_settings))`)
	// replaces the preferences and keeps the launcher-owned terminal flag.
	reloaded := codextea.SettingsWriteResult{
		FeatureSettings:   map[string]bool{"foo": false},
		AnimationsEnabled: localSettingsBoolPtr(false),
	}
	replacedSettings, replaced := interactiveLocalSettingsAfterTrustCheck(staged, &reloaded)
	if !replaced {
		t.Fatal("a reloading trust check must replace the staged record")
	}
	if replacedSettings.Tui.AnimationsEnabled == nil || *replacedSettings.Tui.AnimationsEnabled {
		t.Fatalf("trust-check reload did not adopt the reloaded preferences: %#v", replacedSettings.Tui.AnimationsEnabled)
	}
	if !replacedSettings.NoAltScreen {
		t.Fatal("trust-check reload changed the launcher-owned terminal flag")
	}
}

// Rust #51510, counterpart Rust test: `new_thread_keeps_live_settings_after_failed_reload`
// on the app-layer new-thread entry itself (Rust
// `session_lifecycle.rs`: `self.local_settings = local_settings`). The entry the
// TUI bootstrap uses must carry the resolved preferences plus the launcher-owned
// terminal flag, and must not hand the Model anything derived from a failed
// reload.
func TestInteractiveNewThreadLocalSettingsLikeRust(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	localSettingsWriteConfig(t, home, "[tui]\nshow_tooltips = false\n")

	root := &cli.RootOptions{}
	root.Shared.NoAltScreen = true

	got := interactiveNewThreadLocalSettings(root)
	if !got.NoAltScreen {
		t.Fatal("new-thread record must keep the launcher-owned terminal flag")
	}
	if got.Tui.ShowTooltips == nil || *got.Tui.ShowTooltips {
		t.Fatalf("new-thread record must carry the resolved preferences: %#v", got.Tui.ShowTooltips)
	}
	// With a readable configuration the new-thread entry stages exactly what the
	// startup resolve produces (no behavior change on the happy path).
	if want := interactiveTUISettings(root); !reflect.DeepEqual(got.Tui, want) {
		t.Fatalf("new-thread record diverged from the startup resolve:\ngot  %#v\nwant %#v", got.Tui, want)
	}

	// A broken config.toml cannot turn the entry into anything but the live
	// record it already held; it must not panic and must not synthesize a record
	// from the failed reload.
	localSettingsWriteConfig(t, home, "[broken")
	degraded := interactiveNewThreadLocalSettings(root)
	if !reflect.DeepEqual(degraded.Tui, codextea.SettingsWriteResult{}) {
		t.Fatalf("failed reload produced a record from a stale resolve: %#v", degraded.Tui)
	}
	if !degraded.NoAltScreen {
		t.Fatal("failed reload changed the launcher-owned terminal flag")
	}
}
