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
	return m.writeSettings(settingsWriteKindStatusLineProvider, []SettingsEdit{{
		KeyPath: statusLineProviderConfigKey,
		Value:   selection.ConfigValue(),
	}})
}

// StatusLineProvider returns the live local selection for hosts that need to
// seed a picker. The returned value is a copy.
func (m *Model) StatusLineProvider() *config.StatusLineProviderSelection {
	if m == nil {
		return nil
	}
	return m.statusLineProvider.Clone()
}
