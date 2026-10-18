package tea

import (
	"strings"

	bubbletea "github.com/charmbracelet/bubbletea"

	chatwidget "codex_go/tui/chatwidget"
)

// Rust parity: codex-rs/tui/src/app/tui_mode_picker.rs and
// chatwidget/tui_mode_picker.rs (#46883). The picker persists the preference
// for the next launch without changing this session's terminal ownership.

func (m *Model) openTUIModePicker() {
	if m == nil {
		return
	}
	m.openSelectionViewModal(ModalKindGeneric, chatwidget.NewTUIModePickerView(m.fullscreenTranscript))
}

func (m *Model) applyTUIModeModalOption(optionID string) bubbletea.Cmd {
	if m == nil {
		return nil
	}
	switch strings.TrimSpace(optionID) {
	case chatwidget.TUIModeScrollback:
		return m.saveFullscreenTranscript(false)
	case chatwidget.TUIModeFullscreen:
		return m.saveFullscreenTranscript(true)
	default:
		return nil
	}
}

func (m *Model) saveFullscreenTranscript(enabled bool) bubbletea.Cmd {
	if m == nil {
		return nil
	}
	if m.onWriteSettings == nil {
		m.notice = "Failed to save TUI mode: settings are unavailable"
		m.refreshTranscript()
		return nil
	}
	m.tuiModeWrite = &enabled
	return m.writeSettings(settingsWriteKindTUIMode, []SettingsEdit{{
		KeyPath: "tui.fullscreen_transcript",
		Value:   enabled,
	}})
}

func (m *Model) applyTUIModeWriteResult(success bool, errMessage string) {
	if m == nil {
		return
	}
	if !success {
		m.tuiModeWrite = nil
		m.notice = "Failed to save TUI mode: " + strings.TrimSpace(errMessage)
		m.refreshTranscript()
		return
	}
	if m.tuiModeWrite != nil {
		m.fullscreenTranscript = *m.tuiModeWrite
		m.tuiModeWrite = nil
	}
	mode := "Scrollback"
	if m.fullscreenTranscript {
		mode = "Fullscreen"
	}
	m.addInfoHistoryMessage("Saved TUI mode: " + mode + ". Restart Codex to apply; launch overrides still apply.")
	m.refreshTranscript()
}
