package tea

import (
	"strings"

	bubbletea "github.com/charmbracelet/bubbletea"

	"codex_go/protocol"
	codextui "codex_go/tui"
	historycell "codex_go/tui/history_cell"
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
	selectedThreadID := m.agentPickerSelectedThreadID()
	if reader == nil {
		entries := normalizeAgentEntries(m.agentItems, currentThreadID)
		if len(entries) > 0 {
			m.showAgentPicker(entries, currentThreadID, selectedThreadID)
		} else {
			m.showAgentInfo("No agents available yet.")
		}
		return nil
	}
	entries := normalizeAgentEntries(m.agentItems, currentThreadID)
	if len(entries) > 0 {
		m.showAgentPicker(entries, currentThreadID, selectedThreadID)
	} else {
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
	}
	if m.pendingAgentRefreshRequestID != 0 {
		return nil
	}
	m.nextAgentRefreshRequestID++
	requestID := m.nextAgentRefreshRequestID
	m.pendingAgentRefreshRequestID = requestID
	m.pendingAgentRefreshThreadID = currentThreadID
	return func() bubbletea.Msg {
		entries, err := reader(currentThreadID)
		return AgentListResultMsg{CurrentThreadID: currentThreadID, RequestID: requestID, Entries: entries, Err: err}
	}
}

func (m *Model) applyAgentListResult(message AgentListResultMsg) {
	if m == nil {
		return
	}
	if message.RequestID != 0 {
		if message.RequestID != m.pendingAgentRefreshRequestID || strings.TrimSpace(message.CurrentThreadID) != m.pendingAgentRefreshThreadID {
			return
		}
		m.pendingAgentRefreshRequestID = 0
		m.pendingAgentRefreshThreadID = ""
	}
	currentThreadID := currentAgentThreadID(m, message.CurrentThreadID)
	if currentThreadID != strings.TrimSpace(message.CurrentThreadID) && !agentEntriesContainThread(message.Entries, currentThreadID) {
		return
	}
	if message.Err != nil {
		if len(m.agentItems) == 0 {
			m.modal = nil
			m.addErrorHistoryMessage("Failed to load agent threads: " + strings.TrimSpace(message.Err.Error()))
			m.refreshTranscript()
		}
		return
	}
	entries := normalizeAgentEntries(message.Entries, message.CurrentThreadID)
	if len(entries) == 0 {
		if len(m.agentItems) == 0 {
			m.modal = nil
			m.showAgentInfo("No agents available yet.")
		}
		return
	}
	m.agentItems = append([]codextui.AgentThreadEntry(nil), entries...)
	selectedID := ""
	showPicker := false
	if m.modal != nil {
		showPicker = m.modal.id == "agent-picker" || m.modal.id == "agent-picker-loading"
		if m.modal.id == "agent-picker" && m.modal.selected >= 0 && m.modal.selected < len(m.modal.options) {
			selectedID = strings.TrimSpace(m.modal.options[m.modal.selected].ID)
		}
	}
	if showPicker {
		m.showAgentPicker(entries, currentThreadID, selectedID)
	}
}

func (m *Model) showAgentPicker(entries []codextui.AgentThreadEntry, currentThreadID string, selectedThreadID string) {
	if m == nil || len(entries) == 0 {
		return
	}
	options := make([]ModalOption, 0, len(entries))
	selected := 0
	for i, entry := range entries {
		threadID := strings.TrimSpace(entry.ThreadID)
		if threadID == strings.TrimSpace(selectedThreadID) || (strings.TrimSpace(selectedThreadID) == "" && threadID == currentThreadID) {
			selected = i
		}
		options = append(options, ModalOption{
			ID:          threadID,
			Label:       agentPickerLabel(entry),
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

func agentEntriesContainThread(entries []codextui.AgentThreadEntry, threadID string) bool {
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return false
	}
	for _, entry := range entries {
		if strings.TrimSpace(entry.ThreadID) == threadID {
			return true
		}
	}
	return false
}

func (m *Model) agentPickerSelectedThreadID() string {
	if m == nil || m.modal == nil || m.modal.id != "agent-picker" || m.modal.selected < 0 || m.modal.selected >= len(m.modal.options) {
		return ""
	}
	return strings.TrimSpace(m.modal.options[m.modal.selected].ID)
}

func (m *Model) resetAgentPickerRefresh(clearEntries bool) {
	if m == nil {
		return
	}
	m.nextAgentRefreshRequestID++
	m.pendingAgentRefreshRequestID = 0
	m.pendingAgentRefreshThreadID = ""
	if clearEntries {
		m.agentItems = nil
		m.activeAgentLabel = ""
		m.clearBackgroundThreadEvents()
	}
}

func (m *Model) clearBackgroundThreadEvents() {
	if m == nil {
		return
	}
	m.backgroundThreadEvents = map[string][]protocol.ThreadEvent{}
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
			m.invalidateAppsScope()
			m.State.SetThreadID(threadID)
			m.State.Messages = nil
			m.State.BumpMessagesRevision()
			m.setStatus("idle")
		}
		m.activeSide = nil
		m.setActiveAgentLabel(entry)
		m.notice = entry.DisplayLabel()
		m.refreshTranscript()
		return m.refreshStatusControlsCmd()
	}
	m.notice = "Switching to " + entry.DisplayLabel() + "..."
	activeSide := m.activeSide
	closer := m.onCloseSide
	return func() bubbletea.Msg {
		if activeSide != nil && closer != nil {
			_, err := closer(SideCloseParams{ParentThreadID: activeSide.ParentThreadID, SideThreadID: activeSide.SideThreadID})
			if err != nil {
				return AgentSwitchResultMsg{ThreadID: threadID, Err: err}
			}
		}
		response, err := switcher(threadID)
		return AgentSwitchResultMsg{ThreadID: threadID, Response: response, Err: err, closedSide: activeSide}
	}
}

func (m *Model) applyAgentSwitchResult(message AgentSwitchResultMsg) {
	if m == nil {
		return
	}
	if message.Err != nil {
		if message.closedSide != nil {
			side := message.closedSide
			m.activeSide = nil
			if m.State != nil {
				m.State.SetThreadID(side.ParentThreadID)
				m.State.Messages = cloneSideMessages(side.ParentMessages)
				m.State.BumpMessagesRevision()
				m.setStatus(firstNonEmpty(side.ParentStatus, "idle"))
			}
		}
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
	m.invalidateAppsScope()
	if m.State != nil {
		m.State.SetThreadID(entry.ThreadID)
		messages := append([]codextui.Message(nil), message.Response.Messages...)
		// Only replay the buffer when the persisted thread is empty (the typical
		// running-agent case). Completed content is authoritative once persisted,
		// and replaying item events on top of it would duplicate messages.
		if len(messages) == 0 {
			buffered := m.backgroundThreadEvents[entry.ThreadID]
			for _, event := range buffered {
				messages = applyBufferedThreadEventToMessages(messages, event)
			}
		}
		delete(m.backgroundThreadEvents, entry.ThreadID)
		m.State.Messages = messages
		m.State.BumpMessagesRevision()
		status := strings.TrimSpace(message.Response.Status)
		if status == "" {
			status = "idle"
		}
		m.setStatus(status)
	}
	m.activeSide = nil
	m.upsertAgentEntry(entry)
	m.setActiveAgentLabel(entry)
	m.notice = entry.DisplayLabel()
	m.refreshTranscript()
}

func (m *Model) navigateAgent(direction int) bubbletea.Cmd {
	if m == nil || m.State == nil || direction == 0 {
		return nil
	}
	currentThreadID := strings.TrimSpace(m.State.ThreadID)
	entries := normalizeAgentEntries(m.agentItems, currentThreadID)
	if len(entries) > 1 {
		m.agentItems = append([]codextui.AgentThreadEntry(nil), entries...)
		return m.switchAdjacentAgent(entries, currentThreadID, direction)
	}
	if m.onReadAgents == nil || currentThreadID == "" {
		return nil
	}
	reader := m.onReadAgents
	return func() bubbletea.Msg {
		loaded, err := reader(currentThreadID)
		return AgentNavigateResultMsg{CurrentThreadID: currentThreadID, Entries: loaded, Direction: direction, Err: err}
	}
}

func (m *Model) applyAgentNavigateResult(message AgentNavigateResultMsg) bubbletea.Cmd {
	if m == nil || m.State == nil {
		return nil
	}
	if message.Err != nil {
		m.notice = "Failed to load agent threads: " + strings.TrimSpace(message.Err.Error())
		m.refreshTranscript()
		return nil
	}
	entries := normalizeAgentEntries(message.Entries, message.CurrentThreadID)
	m.agentItems = append([]codextui.AgentThreadEntry(nil), entries...)
	if len(entries) <= 1 {
		return nil
	}
	return m.switchAdjacentAgent(entries, currentAgentThreadID(m, message.CurrentThreadID), message.Direction)
}

func (m *Model) switchAdjacentAgent(entries []codextui.AgentThreadEntry, currentThreadID string, direction int) bubbletea.Cmd {
	if m == nil || len(entries) <= 1 || direction == 0 {
		return nil
	}
	current := 0
	for i := range entries {
		if strings.TrimSpace(entries[i].ThreadID) == strings.TrimSpace(currentThreadID) {
			current = i
			break
		}
	}
	next := (current + direction) % len(entries)
	if next < 0 {
		next += len(entries)
	}
	return m.applyAgentModalOption(entries[next].ThreadID)
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

func agentPickerLabel(entry codextui.AgentThreadEntry) string {
	label := ""
	if !entry.IsPrimary {
		label = strings.TrimSpace(entry.AgentPath)
	}
	if label == "" {
		label = strings.TrimSpace(entry.DisplayLabel())
	}
	if label == "" {
		label = "Agent"
	}
	return "\u25cf " + label
}

func agentPickerDescription(entry codextui.AgentThreadEntry) string {
	return strings.TrimSpace(entry.ThreadID)
}

func agentPickerSubtitle() string {
	return "Select an agent to watch. alt+left previous, alt+right next."
}

func (m *Model) showAgentInfo(message string) {
	if m == nil {
		return
	}
	m.addHistoryCell(historycell.NewInfoEvent(strings.TrimSpace(message), ""))
	m.notice = ""
	m.refreshTranscript()
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
