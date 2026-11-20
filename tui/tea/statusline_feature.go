package tea

import "codex_go/features"

// customStatuslineFeatureKey gates the experimental MCP status line providers
// (Rust codex_features::Feature::TuiExtensionsStatusLine).
const customStatuslineFeatureKey = "tui_extensions_statusline"

// customStatuslineEnabled reports the custom status line gate the TUI captured
// from the connected app server's effective configuration at launch. Rust
// #62387 (tui/src/app/startup.rs statusline_enabled plus
// ChatWidget::custom_statusline_enabled) replaced the live client-config gate
// with that startup value, so saving the /experimental toggle or reloading
// local status line preferences keeps the current launch's gate until the TUI
// restarts.
func (m *Model) customStatuslineEnabled() bool {
	if m == nil {
		return false
	}
	return m.statuslineFeatureEnabled
}

// statuslineFeatureEnabledForLaunch resolves the gate NewModel freezes. A caller
// that already read the connected app server's effective configuration passes
// the resolved value, so a failed read stays off (Rust #62387
// `startup_config.as_ref().is_some_and(...)`) instead of falling back to the
// TUI client's own configuration file.
func statuslineFeatureEnabledForLaunch(options Options) bool {
	if options.StatuslineFeatureEnabled != nil {
		return *options.StatuslineFeatureEnabled
	}
	return StatuslineFeatureEnabledFromConfig(options.FeatureSettings)
}

// StatuslineFeatureEnabledFromConfig resolves the launch gate from an effective
// configuration snapshot. Rust reads `features.tui_extensions_statusline` out of
// the app server's config/read response and treats a missing value as off, so a
// caller that already surfaced that snapshot passes it here instead of leaving
// NewModel to fall back to the TUI client's own configuration.
func StatuslineFeatureEnabledFromConfig(featureSettings map[string]bool) bool {
	return features.Enabled(featureSettings, customStatuslineFeatureKey)
}
