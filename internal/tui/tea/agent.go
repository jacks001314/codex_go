package tea

import (
	"strings"

	bubbletea "github.com/charmbracelet/bubbletea"

	codextui "codex_go/internal/tui"
)

func (m *Model) applyAgentCommand() bubbletea.Cmd {
	if m == nil {
		return nil
	}
	currentThreadID := ""
	if m.State != nil {
		currentThreadID = strings.TrimSpace(m.State.ThreadID)
	}
	reader := m.onReadAgents
	if reader == nil {
		m.openAgentPickerPlaceholder()
		return nil
	}
	m.openModal(ModalRequestMsg{
		ID:    "agent-picker-loading",
		Kind:  ModalKindAgent,
		Title: "Subagents",
		Body:  "Loading agent threads...",
		Options: []ModalOption{{
			ID:       "loading",
			Label:    "Loading",
			Disabled: true,
		}},
	})
	return func() bubbletea.Msg {
		entries, err := reader(currentThreadID)
		return AgentListResultMsg{CurrentThreadID: currentThreadID, Entries: entries, Err: err}
	}
}

func (m *Model) applyAgentListResult(message AgentListResultMsg) {
	if m == nil {
		return
	}
	if message.Err != nil {
		m.openModal(ModalRequestMsg{
			ID:    "agent-picker-error",
			Kind:  ModalKindAgent,
			Title: "Subagents",
			Body:  "Failed to load agent threads: " + strings.TrimSpace(message.Err.Error()),
			Options: []ModalOption{{
				ID:    "ok",
				Label: "OK",
			}},
		})
		return
	}
	entries := normalizeAgentEntries(message.Entries, message.CurrentThreadID)
	if len(entries) == 0 {
		m.openModal(ModalRequestMsg{
			ID:    "agent-picker-empty",
			Kind:  ModalKindAgent,
			Title: "Subagents",
			Body:  "No agents available yet.",
			Options: []ModalOption{{
				ID:    "ok",
				Label: "OK",
			}},
		})
		return
	}
	m.agentItems = append([]codextui.AgentThreadEntry(nil), entries...)
	options := make([]ModalOption, 0, len(entries))
	selected := 0
	currentThreadID := currentAgentThreadID(m, message.CurrentThreadID)
	for i, entry := range entries {
		if strings.TrimSpace(entry.ThreadID) == currentThreadID {
			selected = i
		}
		options = append(options, ModalOption{
			ID:          strings.TrimSpace(entry.ThreadID),
			Label:       agentPickerLabel(entry, strings.TrimSpace(entry.ThreadID) == currentThreadID),
			Description: agentPickerDescription(entry),
			Shortcut:    modelPickerShortcut(i),
			Disabled:    strings.TrimSpace(entry.ThreadID) == "",
		})
	}
	m.openModal(ModalRequestMsg{
		ID:      "agent-picker",
		Kind:    ModalKindAgent,
		Title:   "Subagents",
		Body:    agentPickerSubtitle(),
		Options: options,
	})
	if m.modal != nil {
		m.modal.selected = selected
	}
}

func (m *Model) applyAgentModalOption(optionID string) bubbletea.Cmd {
	if m == nil {
		return nil
	}
	threadID := strings.TrimSpace(optionID)
	if threadID == "" || threadID == "ok" || threadID == "loading" {
		m.notice = "Subagents"
		return nil
	}
	entry, ok := m.agentEntry(threadID)
	if !ok {
		entry = codextui.AgentThreadEntry{ThreadID: threadID}
	}
	if m.State != nil && strings.TrimSpace(m.State.ThreadID) == threadID {
		m.setActiveAgentLabel(entry)
		m.notice = "Already showing " + entry.DisplayLabel()
		return nil
	}
	switcher := m.onSwitchAgent
	if switcher == nil {
		if m.State != nil {
			m.State.SetThreadID(threadID)
			m.State.Messages = nil
			m.State.SetStatus("idle")
		}
		m.activeSide = nil
		m.setActiveAgentLabel(entry)
		m.notice = entry.DisplayLabel()
		m.refreshTranscript()
		return m.refreshStatusControlsCmd()
	}
	m.notice = "Switching to " + entry.DisplayLabel() + "..."
	return func() bubbletea.Msg {
		response, err := switcher(threadID)
		return AgentSwitchResultMsg{ThreadID: threadID, Response: response, Err: err}
	}
}

func (m *Model) applyAgentSwitchResult(message AgentSwitchResultMsg) {
	if m == nil {
		return
	}
	if message.Err != nil {
		text := strings.TrimSpace(message.Err.Error())
		if text == "" {
			text = "unknown error"
		}
		m.notice = "Agent switch failed: " + text
		if m.State != nil {
			m.State.AddMessage(codextui.RoleSystem, "Agent switch failed: "+text)
		}
		m.refreshTranscript()
		return
	}
	entry := message.Response.Entry
	if strings.TrimSpace(entry.ThreadID) == "" {
		entry.ThreadID = strings.TrimSpace(message.ThreadID)
	}
	if strings.TrimSpace(entry.ThreadID) == "" {
		m.notice = "Agent switch failed: missing thread id"
		return
	}
	if m.State != nil {
		m.State.SetThreadID(entry.ThreadID)
		m.State.Messages = append([]codextui.Message(nil), message.Response.Messages...)
		status := strings.TrimSpace(message.Response.Status)
		if status == "" {
			status = "idle"
		}
		m.State.SetStatus(status)
	}
	m.activeSide = nil
	m.upsertAgentEntry(entry)
	m.setActiveAgentLabel(entry)
	m.notice = entry.DisplayLabel()
	m.refreshTranscript()
}

func normalizeAgentEntries(entries []codextui.AgentThreadEntry, currentThreadID string) []codextui.AgentThreadEntry {
	currentThreadID = strings.TrimSpace(currentThreadID)
	out := make([]codextui.AgentThreadEntry, 0, len(entries)+1)
	seen := map[string]bool{}
	for _, entry := range entries {
		entry.ThreadID = strings.TrimSpace(entry.ThreadID)
		entry.AgentNickname = strings.TrimSpace(entry.AgentNickname)
		entry.AgentRole = strings.TrimSpace(entry.AgentRole)
		entry.AgentPath = strings.TrimSpace(entry.AgentPath)
		if entry.ThreadID == "" || seen[entry.ThreadID] {
			continue
		}
		if currentThreadID != "" && entry.ThreadID == currentThreadID {
			entry.IsPrimary = entry.IsPrimary || !hasPrimaryAgentEntry(entries)
		}
		seen[entry.ThreadID] = true
		out = append(out, entry)
	}
	if currentThreadID != "" && !seen[currentThreadID] {
		out = append([]codextui.AgentThreadEntry{{
			ThreadID:  currentThreadID,
			IsPrimary: true,
		}}, out...)
	}
	return out
}

func hasPrimaryAgentEntry(entries []codextui.AgentThreadEntry) bool {
	for _, entry := range entries {
		if entry.IsPrimary {
			return true
		}
	}
	return false
}

func currentAgentThreadID(m *Model, fallback string) string {
	if m != nil && m.State != nil {
		if threadID := strings.TrimSpace(m.State.ThreadID); threadID != "" {
			return threadID
		}
	}
	return strings.TrimSpace(fallback)
}

func agentPickerLabel(entry codextui.AgentThreadEntry, current bool) string {
	label := strings.TrimSpace(entry.DisplayLabel())
	if label == "" {
		label = "Agent"
	}
	if current {
		label += " (current)"
	}
	return label
}

func agentPickerDescription(entry codextui.AgentThreadEntry) string {
	parts := []string{}
	if entry.IsRunning {
		parts = append(parts, "running")
	}
	if entry.IsClosed {
		parts = append(parts, "closed")
	}
	if strings.TrimSpace(entry.AgentPath) != "" {
		parts = append(parts, "path: "+strings.TrimSpace(entry.AgentPath))
	}
	if strings.TrimSpace(entry.ThreadID) != "" {
		parts = append(parts, "thread: "+strings.TrimSpace(entry.ThreadID))
	}
	return strings.Join(parts, "  ")
}

func agentPickerSubtitle() string {
	return "Choose the active agent thread."
}

func (m *Model) agentEntry(threadID string) (codextui.AgentThreadEntry, bool) {
	threadID = strings.TrimSpace(threadID)
	if m == nil || threadID == "" {
		return codextui.AgentThreadEntry{}, false
	}
	for _, entry := range m.agentItems {
		if strings.TrimSpace(entry.ThreadID) == threadID {
			return entry, true
		}
	}
	return codextui.AgentThreadEntry{}, false
}

func (m *Model) upsertAgentEntry(entry codextui.AgentThreadEntry) {
	if m == nil || strings.TrimSpace(entry.ThreadID) == "" {
		return
	}
	entry.ThreadID = strings.TrimSpace(entry.ThreadID)
	for i := range m.agentItems {
		if strings.TrimSpace(m.agentItems[i].ThreadID) == entry.ThreadID {
			m.agentItems[i] = entry
			return
		}
	}
	m.agentItems = append(m.agentItems, entry)
}

func (m *Model) setActiveAgentLabel(entry codextui.AgentThreadEntry) {
	if m == nil {
		return
	}
	label := strings.TrimSpace(entry.DisplayLabel())
	if !entry.IsPrimary && strings.TrimSpace(entry.AgentPath) != "" {
		label = strings.TrimSpace(entry.AgentPath)
	}
	if len(m.agentItems) <= 1 {
		label = ""
	}
	m.activeAgentLabel = label
	if m.statusControls != nil {
		m.statusControls.SetActiveAgentLabel(label, label != "")
	}
}
