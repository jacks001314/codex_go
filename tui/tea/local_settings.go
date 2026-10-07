// Client-owned local settings for the TUI and the configuration-reload flow
// that preserves them (Rust #51510, "Preserve live TUI settings when
// configuration reloads fail").
//
// Rust (codex-rs/tui/src/local_settings.rs) keeps the client-owned preferences in
// a `LocalSettings` record beside the resolved `Config`:
//
//   - `LocalSettings::from(&Config)` resolves the editable preferences from the
//     effective configuration.
//   - `LocalSettings::reloaded(&self, &Config)` adopts the freshly reloaded
//     preferences but restores the fields owned by this launch
//     (`transcript_mode`, `tui.alternate_screen`), so reloading preferences can
//     never change the terminal ownership of the running TUI.
//   - `App::load_new_session_config` returns `(Config, LocalSettings)`; when the
//     reload fails it returns `(self.config.clone(), self.local_settings.clone())`
//     — the live settings, not settings re-derived from the stale in-memory
//     configuration — and the new-thread path installs exactly those staged
//     settings (`session_lifecycle.rs`: `self.local_settings = local_settings`).
//   - `App::confirm_directory_trust` returns `Result<Option<LocalSettings>>`, so
//     the staged record is replaced only when the trust check actually reloaded
//     configuration (resume_config.rs / agents_overview.rs).
//
// Go counterpart (verified against the current tree):
//
//   - The client-owned preference set is `SettingsWriteResult` (tui/tea/model.go),
//     the Go shape of Rust's `Tui`: rendering, effects, animations,
//     status-line colors, question-esc-back, auto-recap, tooltips, theme, pet,
//     session-picker view, notification settings, ... A nil pointer field keeps
//     the live value when the record is applied.
//   - Go has no transcript mode (`grep -rn "transcriptMode\|TranscriptMode"
//     --include='*.go' tui/` is empty); the other launcher-owned field,
//     `tui.alternate_screen`, maps to `Model.noAltScreen`, which is set once from
//     the launch options.
//   - Go resolves local preferences in the host (`app/interactive.go`
//     interactiveLoadSettings / interactiveSettingsFromConfig) and hands the
//     resulting record to the TUI, which stages it through
//     `applySettingsWriteResult` — the only writer of the live preference
//     fields. That write path is therefore Go's configuration-reload boundary,
//     and this file carries the Rust contract it must honour.
package tea

// SettingsReloadOutcome classifies one configuration-reload attempt for the
// TUI's local settings (Rust #51510). Rust's `confirm_directory_trust` reports a
// check that did not reload configuration as `Ok(None)`, a successful reload as
// `Ok(Some(local_settings))`, and a failed reload as an error.
type SettingsReloadOutcome int

const (
	// SettingsReloadNone: no reload happened (Rust Ok(None)); the staged local
	// settings stay untouched.
	SettingsReloadNone SettingsReloadOutcome = iota
	// SettingsReloadOK: the reload succeeded; its preferences are staged.
	SettingsReloadOK
	// SettingsReloadFailed: the reload failed (for example an invalid
	// config.toml); the live settings are kept instead of preferences derived
	// from the stale in-memory configuration.
	SettingsReloadFailed
)

// SettingsReloadOutcomeFor classifies a reload result: a non-nil error means the
// reload failed, a nil result without an error means no reload happened (Rust
// `confirm_directory_trust`'s `Ok(None)`), anything else staged usable settings.
func SettingsReloadOutcomeFor(reloaded *SettingsWriteResult, err error) SettingsReloadOutcome {
	switch {
	case err != nil:
		return SettingsReloadFailed
	case reloaded == nil:
		return SettingsReloadNone
	default:
		return SettingsReloadOK
	}
}

// ShouldStageReloadedLocalSettings reports whether a configuration reload's
// result may be staged onto the live TUI settings (Rust #51510). The live
// settings must survive a failed reload, so only a successful reload stages.
func ShouldStageReloadedLocalSettings(reloaded *SettingsWriteResult, err error) bool {
	return SettingsReloadOutcomeFor(reloaded, err) == SettingsReloadOK
}

// LocalSettings is the staged record of this launcher's client-owned preferences
// (Rust `LocalSettings`, #51510). Server thread responses never refresh it: only
// a successful reload of the local configuration replaces it.
type LocalSettings struct {
	// Tui is the resolved client-owned preference set (Rust `LocalSettings::tui`).
	// Applying it keeps every live value the record does not carry.
	Tui SettingsWriteResult
	// AlternateScreen records this launch's alternate-screen ownership. Rust
	// restores `transcript_mode` and `tui.alternate_screen` across reloads; Go
	// carries the launcher-owned half as Model.noAltScreen.
	AlternateScreen bool
}

// LocalSettingsFromLive builds the live record from the TUI's launch state, the
// Go counterpart of Rust's initial `LocalSettings::from(&config)` (the host
// resolves the preferences and the TUI stages them).
func LocalSettingsFromLive(tui SettingsWriteResult, alternateScreen bool) LocalSettings {
	return LocalSettings{Tui: tui, AlternateScreen: alternateScreen}
}

// Reloaded mirrors Rust `LocalSettings::reloaded(&config)` (#51510): adopt the
// preferences resolved by a successful configuration reload while restoring the
// fields owned by this launch, so reloading preferences never changes the
// terminal ownership of the running TUI.
func (l LocalSettings) Reloaded(reloaded SettingsWriteResult) LocalSettings {
	return LocalSettings{Tui: reloaded, AlternateScreen: l.AlternateScreen}
}

// LoadNewSessionLocalSettings mirrors Rust `App::load_new_session_config`
// (#51510): reloading configuration for a new thread carries the live local
// settings forward when the reload fails — Rust returns
// `(self.config.clone(), self.local_settings.clone())` — and stages the reloaded
// preferences when it succeeds. reload is the host's configuration reload (Rust
// `rebuild_config_for_cwd`); a nil reload means the host did not attempt one.
func LoadNewSessionLocalSettings(live LocalSettings, reload func() (SettingsWriteResult, error)) (LocalSettings, error) {
	if reload == nil {
		return live, nil
	}
	reloaded, err := reload()
	if err != nil {
		return live, err
	}
	return live.Reloaded(reloaded), nil
}

// LocalSettingsAfterTrustCheck mirrors the `Result<Option<LocalSettings>>`
// contract of Rust `App::confirm_directory_trust` (#51510): the staged record is
// replaced only when the directory-trust check actually reloaded configuration
// (`Ok(Some(..))`); a check that did not reload (`Ok(None)`, reported here as a
// nil result) leaves it alone. The bool reports whether the record was replaced.
func LocalSettingsAfterTrustCheck(staged LocalSettings, reloaded *SettingsWriteResult) (LocalSettings, bool) {
	if reloaded == nil {
		return staged, false
	}
	return staged.Reloaded(*reloaded), true
}

// ReloadedLocalSettings is the preference-set form of the same decision: a
// successful reload stages its values, a failed or skipped reload keeps the live
// ones (Rust `LocalSettings::reloaded` is only applied on success).
func ReloadedLocalSettings(live, reloaded SettingsWriteResult, outcome SettingsReloadOutcome) SettingsWriteResult {
	if outcome == SettingsReloadOK {
		return reloaded
	}
	return live
}
