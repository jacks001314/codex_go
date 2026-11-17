package tea

import (
	bubbletea "charm.land/bubbletea/v2"

	"codex_go/config"
)

const statusLineProviderConfigKey = "tui.status_line_provider"

// SaveStatusLineProvider persists the local statusline selection and stages the
// reloaded value only after the host write succeeds (#61212).
func (m *Model) SaveStatusLineProvider(selection config.StatusLineProviderSelection) bubbletea.Cmd {
	if m == nil {
		return nil
	}
	if err := selection.Validate(); err != nil {
		m.notice = "Invalid status line provider: " + err.Error()
		m.refreshTranscript()
		return nil
	}
	if m.onWriteSettings == nil {
		m.notice = "Saving the status line provider requires configuration support."
		m.refreshTranscript()
		return nil
	}
	// Rust save_status_line_provider compares the stored selection with the
	// requested one after the write lands; the reloaded record has already
	// replaced the live value by then, so both ends are captured here.
	m.statuslineProviderChange = &statuslineProviderChange{
		from: m.statusLineProvider.Clone(),
		to:   selection,
	}
	return m.writeSettings(settingsWriteKindStatusLineProvider, []SettingsEdit{{
		KeyPath: statusLineProviderConfigKey,
		Value:   selection.ConfigValue(),
	}})
}

// statuslineProviderChange pairs the stored selection with the one a save is
// writing so the success path can name the transition.
type statuslineProviderChange struct {
	from *config.StatusLineProviderSelection
	to   config.StatusLineProviderSelection
}

// recordStatuslineProviderChange reports how the saved selection differs from
// the one the save started from (Rust save_status_line_provider's `change`).
func (m *Model) recordStatuslineProviderChange() {
	if m == nil {
		return
	}
	change := m.statuslineProviderChange
	m.statuslineProviderChange = nil
	if change == nil {
		return
	}
	if kind := statuslineProviderChangeKind(change.from, &change.to); kind != "" {
		m.statuslineMetrics.Record("codex.statusline.provider_changed", map[string]string{"change": kind})
	}
}

// statuslineProviderChangeKind mirrors Rust's match over the stored and new
// selections: enabling a custom provider, returning to the built-in one, or
// switching between two custom providers. Re-saving the same selection is not
// a change.
func statuslineProviderChangeKind(previous, next *config.StatusLineProviderSelection) string {
	previousDefault := previous == nil || previous.Default
	nextDefault := next == nil || next.Default
	switch {
	case previousDefault && !nextDefault:
		return "enabled_custom"
	case !previousDefault && nextDefault:
		return "returned_to_default"
	case !previousDefault && !nextDefault && (previous.Server != next.Server || previous.Tool != next.Tool):
		return "switched_custom"
	default:
		return ""
	}
}

// StatusLineProvider returns the live local selection for hosts that need to
// seed a picker. The returned value is a copy.
func (m *Model) StatusLineProvider() *config.StatusLineProviderSelection {
	if m == nil {
		return nil
	}
	return m.statusLineProvider.Clone()
}
