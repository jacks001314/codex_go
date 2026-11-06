package tea

import (
	"errors"
	"strings"

	bubbletea "charm.land/bubbletea/v2"

	historycell "codex_go/tui/history_cell"
	"codex_go/tui/keymsg"
)

const (
	settingsWriteKindMemories       = "memories"
	settingsWriteKindMemoriesEnable = "memories_enable"
	memoriesDocsURL                 = "https://developers.openai.com/codex/memories"
)

type memoriesModalState struct {
	enablePrompt      bool
	resetConfirmation bool
	useMemories       bool
	generateMemories  bool
	// feature names the opt-in the enable prompt belongs to ("memories" or
	// "multi_agent"); empty means Memories (Rust #43113 shares one prompt).
	feature string
}

func (m *Model) openMemoriesSettings() {
	if m == nil {
		return
	}
	if !m.featureSettings["memories"] {
		// Rust #43113: both opt-ins share the app-server write prompt.
		m.openFeatureEnablePrompt("memories")
		return
	}

	state := &memoriesModalState{
		useMemories:      m.useMemories,
		generateMemories: m.generateMemories,
	}
	m.openModal(ModalRequestMsg{
		ID:         "memories-settings",
		Kind:       ModalKindMemories,
		Title:      "Memories",
		Body:       "Choose how Codex uses and creates memories. Changes are saved to config.toml",
		Options:    memoriesSettingsOptions(state),
		FooterNote: "Learn more: " + memoriesDocsURL,
		FooterHint: "space toggle \u00b7 enter save/select \u00b7 esc cancel",
	})
	m.modal.memories = state
}

func memoriesEnableOptions() []ModalOption {
	return []ModalOption{
		{ID: "enable", Label: "Yes, enable", Description: "Save the setting now. You will need a new session to use it."},
		{ID: "keep_disabled", Label: "Not now", Description: "Keep memories disabled."},
	}
}

func memoriesSettingsOptions(state *memoriesModalState) []ModalOption {
	return []ModalOption{
		{ID: "use_memories", Label: checkboxLabel(state.useMemories, "Use memories"), Description: "Use memories in the following threads. Applied at next thread."},
		{ID: "generate_memories", Label: checkboxLabel(state.generateMemories, "Generate memories"), Description: "Generate memories from the following threads. Current thread included."},
		{ID: "reset_memories", Label: "Reset all memories", Description: "Clear local memory files and summaries. Existing threads stay intact."},
	}
}

func checkboxLabel(enabled bool, label string) string {
	if enabled {
		return "[x] " + label
	}
	return "[ ] " + label
}

func (m *Model) updateMemoriesModal(message keymsg.KeyMsg) bubbletea.Cmd {
	if m == nil || m.modal == nil || m.modal.memories == nil {
		return nil
	}
	state := m.modal.memories
	if state.enablePrompt {
		switch message.Type {
		case keymsg.KeyEsc, keymsg.KeyCtrlC:
			m.modal = nil
			return nil
		case keymsg.KeyUp:
			m.moveModalSelection(-1)
		case keymsg.KeyDown, keymsg.KeyTab:
			m.moveModalSelection(1)
		case keymsg.KeyEnter:
			enable := m.modal.selected == 0
			m.modal = nil
			if enable {
				feature := state.feature
				if strings.TrimSpace(feature) == "" {
					feature = "memories"
				}
				return m.enableFeatureForNewThreads(feature)
			}
		}
		return nil
	}

	if state.resetConfirmation {
		switch message.Type {
		case keymsg.KeyEsc, keymsg.KeyCtrlC:
			m.closeMemoriesResetConfirmation()
		case keymsg.KeyUp:
			m.moveModalSelection(-1)
		case keymsg.KeyDown, keymsg.KeyTab:
			m.moveModalSelection(1)
		case keymsg.KeyEnter:
			if m.modal.selected == 0 {
				m.modal = nil
				return m.resetMemories()
			}
			m.closeMemoriesResetConfirmation()
		}
		return nil
	}

	switch message.Type {
	case keymsg.KeyEsc, keymsg.KeyCtrlC:
		m.modal = nil
	case keymsg.KeyUp:
		m.moveModalSelection(-1)
	case keymsg.KeyDown, keymsg.KeyTab:
		m.moveModalSelection(1)
	case keymsg.KeySpace:
		m.toggleSelectedMemorySetting()
	case keymsg.KeyRunes:
		if string(message.Runes) == " " {
			m.toggleSelectedMemorySetting()
		}
	case keymsg.KeyEnter:
		if m.modal.selected == 2 {
			m.openMemoriesResetConfirmation()
			return nil
		}
		m.modal = nil
		return m.saveMemorySettings(state.useMemories, state.generateMemories)
	}
	return nil
}

func (m *Model) toggleSelectedMemorySetting() {
	if m == nil || m.modal == nil || m.modal.memories == nil {
		return
	}
	state := m.modal.memories
	switch m.modal.selected {
	case 0:
		state.useMemories = !state.useMemories
	case 1:
		state.generateMemories = !state.generateMemories
	default:
		return
	}
	m.modal.options = memoriesSettingsOptions(state)
}

func (m *Model) openMemoriesResetConfirmation() {
	state := m.modal.memories
	state.resetConfirmation = true
	m.modal.title = "Reset all memories?"
	m.modal.body = "This clears local memory files and rollout summaries for the current Codex home."
	m.modal.options = []ModalOption{
		{ID: "reset", Label: "Reset all memories", Description: "Delete local memory files and rollout summaries."},
		{ID: "back", Label: "Go back", Description: "Return to memory settings."},
	}
	m.modal.selected = 0
}

func (m *Model) closeMemoriesResetConfirmation() {
	state := m.modal.memories
	state.resetConfirmation = false
	m.modal.title = "Memories"
	m.modal.body = "Choose how Codex uses and creates memories. Changes are saved to config.toml"
	m.modal.options = memoriesSettingsOptions(state)
	m.modal.selected = 2
}

func (m *Model) saveMemorySettings(useMemories bool, generateMemories bool) bubbletea.Cmd {
	previousGenerate := m.generateMemories
	m.useMemories = useMemories
	m.generateMemories = generateMemories
	requestID := m.nextSettingsRequest()
	m.pendingSettingsRequestID = requestID
	if m.onWriteMemorySettings != nil {
		writer := m.onWriteMemorySettings
		threadID := strings.TrimSpace(m.State.ThreadID)
		return func() bubbletea.Msg {
			result, err := writer(threadID, useMemories, generateMemories, previousGenerate != generateMemories)
			return SettingsWriteResultMsg{RequestID: requestID, Kind: settingsWriteKindMemories, Result: result, Err: err}
		}
	}
	if m.onWriteSettings != nil {
		writer := m.onWriteSettings
		edits := []SettingsEdit{
			{KeyPath: "memories.use_memories", Value: useMemories},
			{KeyPath: "memories.generate_memories", Value: generateMemories},
		}
		return func() bubbletea.Msg {
			result, err := writer(edits)
			return SettingsWriteResultMsg{RequestID: requestID, Kind: settingsWriteKindMemories, Result: result, Err: err}
		}
	}
	m.pendingSettingsRequestID = 0
	m.notice = "Memory settings updated."
	return nil
}

func (m *Model) resetMemories() bubbletea.Cmd {
	if m.onResetMemories == nil {
		return func() bubbletea.Msg {
			return MemoryResetResultMsg{Err: errors.New("memory/reset is unavailable in this runtime")}
		}
	}
	reset := m.onResetMemories
	return func() bubbletea.Msg { return MemoryResetResultMsg{Err: reset()} }
}

func (m *Model) applyMemoryResetResult(message MemoryResetResultMsg) {
	if m == nil {
		return
	}
	// Rust #46910: memory reset output resumes transcript following.
	m.activityFollow = true
	if message.Err != nil {
		m.applyHistoryCell(historycell.NewErrorEvent("Failed to reset memories: " + message.Err.Error()))
		return
	}
	m.applyHistoryCell(historycell.NewInfoEvent("Reset local memories.", ""))
}
