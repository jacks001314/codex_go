// Package-level contract for Rust #51510 ("Preserve live TUI settings when
// configuration reloads fail").
//
// Structural differences from Rust (verified against the current Go tree):
//   - Go has no `LocalSettings` staging type; the live TUI settings are the
//     fields on the tea `Model` (`featureSettings`, `animationsEnabled`,
//     `statusLineUseColors`, `showTooltips`, `effects`, `noAltScreen`, ...),
//     and their only writer is `applySettingsWriteResult` in
//     `tui/tea/settings_commands.go`.
//   - Go has no transcript mode (`grep -rn transcriptMode tui/tea/*.go` returns
//     nothing), so Rust's `LocalSettings::reloaded` restore of `transcript_mode`
//     has no Go counterpart.
//   - The merge semantics are "a nil pointer field keeps the current live
//     value" (see the `SettingsWriteResult` field comments in `tui/tea/model.go`).
//
// The semantics of Rust's `LocalSettings::reloaded` are therefore carried by the
// contract in this file plus the `applySettingsWriteResult` wiring: a reload's
// result may only be staged onto the live settings when the reload actually
// succeeded, otherwise the live settings are preserved untouched. The field Rust
// restores from the launcher (`transcript_mode`; `tui.alternate_screen`) maps to
// `Model.noAltScreen` in Go, which a configuration reload must not change.
package tea

// SettingsReloadOutcome classifies one configuration-reload attempt for the
// TUI's local settings (Rust #51510; Rust `App::confirm_directory_trust`
// returns `Ok(None)` when no reload happened).
type SettingsReloadOutcome int

const (
	// SettingsReloadNone: no reload happened (Rust confirm_directory_trust
	// Ok(None)); staged settings stay untouched.
	SettingsReloadNone SettingsReloadOutcome = iota
	// SettingsReloadOK: reload succeeded; its values are staged.
	SettingsReloadOK
	// SettingsReloadFailed: the reload failed (e.g. invalid config.toml); the
	// live settings are kept instead of settings derived from the stale
	// in-memory configuration.
	SettingsReloadFailed
)

// SettingsReloadOutcomeFor classifies a reload result: a non-nil error means the
// reload failed, a nil result without an error means no reload happened (Rust
// confirm_directory_trust's Ok(None)), and anything else staged usable settings.
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

// ReloadedLocalSettings mirrors Rust `LocalSettings::reloaded` (#51510): stage
// the reloaded values for a successful reload; keep the live settings when the
// reload failed or was not attempted. A failed reload must never write its
// result back into the live TUI settings.
func ReloadedLocalSettings(live, reloaded SettingsWriteResult, outcome SettingsReloadOutcome) SettingsWriteResult {
	if outcome == SettingsReloadOK {
		return reloaded
	}
	return live
}
