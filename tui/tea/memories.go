package tea

import (
	"errors"
	"strings"

	bubbletea "github.com/charmbracelet/bubbletea"

	historycell "codex_go/tui/history_cell"
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
}

func (m *Model) openMemoriesSettings() {
	if m == nil {
		return
	}
	if !m.featureSettings["memories"] {
		m.openModal(ModalRequestMsg{
			ID:         "memories-enable",
			Kind:       ModalKindMemories,
			Title:      "Enable memories?",
			Body:       "Memories are currently disabled in your config.",
			Options:    memoriesEnableOptions(),
			FooterNote: "Learn more: " + memoriesDocsURL,
		})
		m.modal.memories = &memoriesModalState{enablePrompt: true}
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
		FooterHint: "Space to toggle  Enter to save  Esc to cancel",
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

func (m *Model) updateMemoriesModal(message bubbletea.KeyMsg) bubbletea.Cmd {
	if m == nil || m.modal == nil || m.modal.memories == nil {
		return nil
	}
	state := m.modal.memories
	if state.enablePrompt {
		switch message.Type {
		case bubbletea.KeyEsc, bubbletea.KeyCtrlC:
			m.modal = nil
			return nil
		case bubbletea.KeyUp:
			m.moveModalSelection(-1)
		case bubbletea.KeyDown, bubbletea.KeyTab:
			m.moveModalSelection(1)
		case bubbletea.KeyEnter:
			enable := m.modal.selected == 0
			m.modal = nil
			if enable {
				return m.writeSettings(settingsWriteKindMemoriesEnable, []SettingsEdit{{KeyPath: "features.memories", Value: true}})
			}
		}
		return nil
	}

	if state.resetConfirmation {
		switch message.Type {
		case bubbletea.KeyEsc, bubbletea.KeyCtrlC:
			m.closeMemoriesResetConfirmation()
		case bubbletea.KeyUp:
			m.moveModalSelection(-1)
		case bubbletea.KeyDown, bubbletea.KeyTab:
			m.moveModalSelection(1)
		case bubbletea.KeyEnter:
			if m.modal.selected == 0 {
				m.modal = nil
				return m.resetMemories()
			}
			m.closeMemoriesResetConfirmation()
		}
		return nil
	}

	switch message.Type {
	case bubbletea.KeyEsc, bubbletea.KeyCtrlC:
		m.modal = nil
	case bubbletea.KeyUp:
		m.moveModalSelection(-1)
	case bubbletea.KeyDown, bubbletea.KeyTab:
		m.moveModalSelection(1)
	case bubbletea.KeySpace:
		m.toggleSelectedMemorySetting()
	case bubbletea.KeyRunes:
		if string(message.Runes) == " " {
			m.toggleSelectedMemorySetting()
		}
	case bubbletea.KeyEnter:
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
	if message.Err != nil {
		m.applyHistoryCell(historycell.NewErrorEvent("Failed to reset memories: " + message.Err.Error()))
		return
	}
	m.applyHistoryCell(historycell.NewInfoEvent("Reset local memories.", ""))
}
