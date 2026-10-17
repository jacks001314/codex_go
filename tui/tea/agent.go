package tea

import (
	"strings"

	bubbletea "github.com/charmbracelet/bubbletea"

	"codex_go/protocol"
	codextui "codex_go/tui"
	chatwidget "codex_go/tui/chatwidget"
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
		visible := m.visibleAgentEntries(entries)
		if len(visible) > 0 {
			m.showAgentPicker(visible, currentThreadID, selectedThreadID)
		} else {
			m.showAgentInfo("No agents available yet.")
		}
		return nil
	}
	entries := normalizeAgentEntries(m.agentItems, currentThreadID)
	visible := m.visibleAgentEntries(entries)
	if len(visible) > 0 {
		m.showAgentPicker(visible, currentThreadID, selectedThreadID)
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
	return m.refreshAgentPickerThreadsCmd()
}

func (m *Model) refreshAgentPickerThreadsCmd() bubbletea.Cmd {
	if m == nil || m.onReadAgents == nil || m.pendingAgentRefreshRequestID != 0 {
		return nil
	}
	currentThreadID := ""
	if m.State != nil {
		currentThreadID = strings.TrimSpace(m.State.ThreadID)
	}
	if currentThreadID == "" {
		return nil
	}
	reader := m.onReadAgents
	m.nextAgentRefreshRequestID++
	requestID := m.nextAgentRefreshRequestID
	m.pendingAgentRefreshRequestID = requestID
	m.pendingAgentRefreshThreadID = currentThreadID
	return func() bubbletea.Msg {
		entries, err := reader(currentThreadID)
		return AgentListResultMsg{CurrentThreadID: currentThreadID, RequestID: requestID, Entries: entries, Err: err}
	}
}

func (m *Model) applyAgentListResult(message AgentListResultMsg) bubbletea.Cmd {
	if m == nil {
		return nil
	}
	if message.RequestID != 0 {
		if message.RequestID != m.pendingAgentRefreshRequestID || strings.TrimSpace(message.CurrentThreadID) != m.pendingAgentRefreshThreadID {
			return nil
		}
		m.pendingAgentRefreshRequestID = 0
		m.pendingAgentRefreshThreadID = ""
	}
	currentThreadID := currentAgentThreadID(m, message.CurrentThreadID)
	if currentThreadID != strings.TrimSpace(message.CurrentThreadID) &&
		!agentEntriesContainThread(message.Entries, currentThreadID) &&
		!m.agentThreadArchived(currentThreadID) {
		return nil
	}
	if message.Err != nil {
		if len(m.agentItems) == 0 {
			m.modal = nil
			m.addErrorHistoryMessage("Failed to load agent threads: " + strings.TrimSpace(message.Err.Error()))
			m.refreshTranscript()
		}
		if m.agentPickerRefreshFollowUp {
			m.agentPickerRefreshFollowUp = false
			return m.refreshAgentPickerThreadsCmd()
		}
		return nil
	}
	entries := normalizeAgentEntries(message.Entries, message.CurrentThreadID)
	m.updateAgentArchivedVisibility(entries)
	for threadID, archived := range m.agentPickerVisibilityChanged {
		if archived {
			m.archivedAgentThreads[threadID] = true
		} else {
			delete(m.archivedAgentThreads, threadID)
		}
		for index := range entries {
			if strings.TrimSpace(entries[index].ThreadID) == threadID {
				entries[index].Archived = archived
			}
		}
	}
	followUp := m.agentPickerRefreshFollowUp
	if followUp {
		m.agentPickerRefreshFollowUp = false
	} else {
		m.agentPickerVisibilityChanged = nil
	}
	visible := m.visibleAgentEntries(entries)
	if len(visible) == 0 {
		m.agentItems = append([]codextui.AgentThreadEntry(nil), entries...)
		if m.modal != nil && (m.modal.id == "agent-picker" || m.modal.id == "agent-picker-loading") {
			m.modal = nil
			m.showAgentInfo("No agents available yet.")
		}
		if followUp {
			return m.refreshAgentPickerThreadsCmd()
		}
		return nil
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
		m.showAgentPicker(visible, currentThreadID, selectedID)
	}
	if followUp {
		return m.refreshAgentPickerThreadsCmd()
	}
	return nil
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
	m.agentPickerVisibilityChanged = nil
	m.agentPickerRefreshFollowUp = false
	if clearEntries {
		m.agentItems = nil
		m.activeAgentLabel = ""
		m.agentThreadArchiveCause = nil
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
	if m.agentThreadArchived(threadID) {
		m.notice = "This agent is archived. Unarchive it before switching."
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
			m.deactivateSideConversation()
			m.State.SetThreadID(threadID)
			m.State.Messages = nil
			m.State.BumpMessagesRevision()
			m.setStatus("idle")
		}
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
		m.discardPendingAgentsOverviewDraft()
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
		// Rust #48118: replacing the chat widget drops a pending fullscreen
		// right-click paste bound to the previous composer.
		m.resetRightClickPaste()
		// Rust #43994: the switched-to thread must not inherit transcript state
		// (tool-call display, retry, review) from the thread that was active.
		m.resetThreadScopedState()
		m.State.SetThreadID(entry.ThreadID)
		messages := append([]codextui.Message(nil), message.Response.Messages...)
		buffered := m.backgroundThreadEvents[entry.ThreadID]
		// Only replay the buffer when the persisted thread is empty (the typical
		// running-agent case). Completed content is authoritative once persisted,
		// and replaying item events on top of it would duplicate messages.
		// Rust #44969: a read-only snapshot skips buffered request replay so the
		// frozen history is not rewritten by events observed while another app
		// owns the task.
		if len(messages) == 0 && !message.Response.ReadOnly {
			for _, event := range buffered {
				messages = applyBufferedThreadEventToMessages(messages, event)
			}
			// Rust #42903: buffered live notifications can introduce async
			// questions; historical turn replay cannot.
			m.appendBufferedAsyncQuestions(buffered)
		}
		delete(m.backgroundThreadEvents, entry.ThreadID)
		m.State.Messages = messages
		m.State.BumpMessagesRevision()
		status := strings.TrimSpace(message.Response.Status)
		if status == "" {
			status = "idle"
		}
		m.setStatus(status)
		// Rust #43360: restore the switched-to thread's server-reported model
		// and provider.
		if model := strings.TrimSpace(message.Response.Model); model != "" {
			m.State.Model = model
		}
		if provider := strings.TrimSpace(message.Response.Provider); provider != "" {
			m.State.Provider = provider
		}
		// Rust #43921: restore the switched-to turn's active reasoning heading.
		m.resetReasoningSummaryHeader()
		m.setWorkingStatusHeader(message.Response.WorkingStatusHeader)
		m.reasoningResumeTurnID = strings.TrimSpace(message.Response.WorkingReasoningTurnID)
		m.reasoningItemID = strings.TrimSpace(message.Response.WorkingReasoningItemID)
		m.applyRestoredReasoningHeading()
		// A restored active item may have missed earlier deltas; the completed
		// item reconciles the stream (Rust #43921 restore_active_reasoning_item).
		m.reasoningRecoveredAfterRefresh = strings.TrimSpace(message.Response.WorkingReasoningItemID) != "" ||
			strings.TrimSpace(message.Response.WorkingStatusHeader) != ""
		// Rust #43921 ReasoningReplay: when the read snapshot identifies no active
		// reasoning item, restore it from the buffered deltas so the heading
		// survives earlier evictions.
		if m.reasoningItemID == "" && !message.Response.ReadOnly {
			m.replayBufferedReasoning(buffered)
		}
	}
	m.deactivateSideConversation()
	m.upsertAgentEntry(entry)
	m.setActiveAgentLabel(entry)
	m.notice = entry.DisplayLabel()
	// Rust #43253/#43330: the resumed thread owns its settings, and Rust #44969
	// opens a task managed by another app server as a read-only history snapshot
	// instead of refusing to attach.
	if message.Response.ThreadSettings != nil {
		m.applyThreadSettingsValues(*message.Response.ThreadSettings)
	}
	m.setReadOnlyThread(message.Response.ReadOnly)
	m.restorePendingAgentsOverviewDraft()
	m.refreshTranscript()
}

// replayBufferedReasoning restores a switched-to thread's active reasoning item
// from its buffered events when the read snapshot did not identify one (Rust
// #43921 ReasoningReplay). The buffer's own start/complete events carry the
// store's active-reasoning marker, so a started item becomes the active one and
// its completion or a turn boundary clears it; a buffer without an explicit
// reasoning start falls back to the resumed turn's first streamed delta (Rust
// recover_resumed_reasoning).
func (m *Model) replayBufferedReasoning(events []protocol.ThreadEvent) {
	if m == nil || m.reasoningItemID != "" {
		return
	}
	marker, markerSeen := bufferedReasoningMarker(events)
	if markerSeen {
		if marker == "" {
			// The buffer showed the item completing (or its turn ending), so no
			// item owns later deltas and the retained heading stays as-is.
			return
		}
		m.accumulateBufferedReasoningDeltas(events, marker)
		m.reasoningItemID = marker
		m.reasoningResumeTurnID = ""
		m.restoreReasoningStatusHeader()
		return
	}
	// No explicit start was retained: adopt the resumed turn's first streamed
	// reasoning delta so the live heading survives events the bounded buffer
	// already replayed.
	m.accumulateBufferedReasoningDeltas(events, bufferedReasoningDeltaItem(events))
}

// bufferedReasoningMarker mirrors Rust ThreadEventStore's active-reasoning
// marker lifecycle over buffered events: an item/started reasoning item sets
// the marker, and its item/completed (or a turn boundary) clears it. The boolean
// reports whether the buffer tracked an explicit reasoning start at all.
func bufferedReasoningMarker(events []protocol.ThreadEvent) (string, bool) {
	marker := ""
	seen := false
	for _, event := range events {
		switch event.Type {
		case "item.started":
			if itemID := bufferedReasoningItemID(event.Item); itemID != "" {
				marker = itemID
				seen = true
			}
		case "item.completed":
			if itemID := bufferedReasoningItemID(event.Item); itemID != "" && itemID == marker {
				marker = ""
			}
		case "turn.started", "turn.completed", "turn.failed":
			marker = ""
		}
	}
	return marker, seen
}

// bufferedReasoningItemID returns a reasoning item's id.
func bufferedReasoningItemID(item *protocol.ThreadItem) string {
	if item == nil || item.Type != "reasoning" {
		return ""
	}
	return strings.TrimSpace(item.ID)
}

// bufferedReasoningDeltaItem returns the first reasoning item with buffered
// deltas.
func bufferedReasoningDeltaItem(events []protocol.ThreadEvent) string {
	for _, event := range events {
		if event.Type != "item.reasoning.delta" || event.Delta == nil {
			continue
		}
		if itemID := strings.TrimSpace(event.Delta.ItemID); itemID != "" {
			return itemID
		}
	}
	return ""
}

// accumulateBufferedReasoningDeltas appends the item's buffered summary deltas
// and, when they carry a usable line, makes it the live reasoning item.
func (m *Model) accumulateBufferedReasoningDeltas(events []protocol.ThreadEvent, itemID string) {
	if m == nil || itemID == "" {
		return
	}
	for _, event := range events {
		if event.Type != "item.reasoning.delta" || event.Delta == nil {
			continue
		}
		if strings.TrimSpace(event.Delta.ItemID) != itemID {
			continue
		}
		if m.reasoningSummaryBuffers == nil {
			m.reasoningSummaryBuffers = map[string]string{}
		}
		m.reasoningSummaryBuffers[itemID] += event.Delta.Text
	}
	if _, ok := chatwidget.LatestSummaryLine(m.reasoningSummaryBuffers[itemID]); !ok {
		return
	}
	m.reasoningItemID = itemID
	m.restoreReasoningStatusHeader()
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
	entries = m.visibleAgentEntries(entries)
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

func (m *Model) visibleAgentEntries(entries []codextui.AgentThreadEntry) []codextui.AgentThreadEntry {
	if m == nil {
		return entries
	}
	out := make([]codextui.AgentThreadEntry, 0, len(entries))
	for _, entry := range entries {
		if strings.TrimSpace(entry.ThreadID) == "" {
			continue
		}
		if entry.Archived || m.archivedAgentThreads[strings.TrimSpace(entry.ThreadID)] {
			continue
		}
		out = append(out, entry)
	}
	return out
}

func (m *Model) updateAgentArchivedVisibility(entries []codextui.AgentThreadEntry) {
	if m == nil {
		return
	}
	if m.agentThreadArchiveCause == nil {
		m.agentThreadArchiveCause = map[string]string{}
	}
	archived := map[string]bool{}
	for _, entry := range entries {
		threadID := strings.TrimSpace(entry.ThreadID)
		if threadID != "" && entry.Archived {
			archived[threadID] = true
			if m.agentThreadArchiveCause[threadID] == "" {
				m.agentThreadArchiveCause[threadID] = "self"
			}
		} else if threadID != "" {
			delete(m.agentThreadArchiveCause, threadID)
		}
	}
	m.archivedAgentThreads = archived
}

func (m *Model) agentThreadArchived(threadID string) bool {
	if m == nil {
		return false
	}
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return false
	}
	return m.archivedAgentThreads[threadID]
}

// setAgentPickerThreadVisibility updates picker visibility after a local archive or
// unarchive, preserving the selected thread id while rows change.
func (m *Model) setAgentPickerThreadVisibility(threadID string, archived bool) bubbletea.Cmd {
	if m == nil {
		return nil
	}
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return nil
	}
	_, cached := m.agentEntry(threadID)
	if m.archivedAgentThreads == nil {
		m.archivedAgentThreads = map[string]bool{}
	}
	if m.agentThreadArchiveCause == nil {
		m.agentThreadArchiveCause = map[string]string{}
	}
	closure := m.agentThreadVisibilityClosure(threadID)
	for _, visibleID := range closure {
		if archived {
			m.archivedAgentThreads[visibleID] = true
			if visibleID == threadID {
				m.agentThreadArchiveCause[visibleID] = "self"
			} else if m.agentThreadArchiveCause[visibleID] == "" {
				m.agentThreadArchiveCause[visibleID] = "parent:" + threadID
			}
		} else {
			cause := m.agentThreadArchiveCause[visibleID]
			if visibleID == threadID || cause == "parent:"+threadID || !m.archivedAgentThreads[visibleID] {
				delete(m.agentThreadArchiveCause, visibleID)
				delete(m.archivedAgentThreads, visibleID)
			} else {
				continue
			}
		}
		for i := range m.agentItems {
			if strings.TrimSpace(m.agentItems[i].ThreadID) == visibleID {
				m.agentItems[i].Archived = archived
				if archived {
					m.agentItems[i].IsRunning = false
				}
			}
		}
		if archived {
			delete(m.backgroundThreadEvents, visibleID)
		}
	}
	if m.pendingAgentRefreshRequestID != 0 {
		if m.agentPickerVisibilityChanged == nil {
			m.agentPickerVisibilityChanged = map[string]bool{}
		}
		for _, visibleID := range closure {
			m.agentPickerVisibilityChanged[visibleID] = archived
		}
		m.agentPickerRefreshFollowUp = true
	}
	m.refreshAgentPickerAfterVisibilityChange()
	if !archived && !cached {
		return m.refreshAgentPickerThreadsCmd()
	}
	return nil
}

// agentThreadVisibilityClosure returns the thread and every cached descendant.
// Archive/unarchive is a tree operation; the server refresh remains
// authoritative for rows it has not loaded yet.
func (m *Model) agentThreadVisibilityClosure(threadID string) []string {
	threadID = strings.TrimSpace(threadID)
	if m == nil || threadID == "" {
		return nil
	}
	out := []string{threadID}
	seen := map[string]bool{threadID: true}
	for changed := true; changed; {
		changed = false
		for _, entry := range m.agentItems {
			child := strings.TrimSpace(entry.ThreadID)
			parent := strings.TrimSpace(entry.ParentThreadID)
			if child == "" || parent == "" || seen[child] || !seen[parent] {
				continue
			}
			seen[child] = true
			out = append(out, child)
			changed = true
		}
	}
	return out
}

func (m *Model) visibleThreads() []codextui.AgentThreadEntry {
	if m == nil {
		return nil
	}
	return m.visibleAgentEntries(m.agentItems)
}

func (m *Model) refreshAgentPickerAfterVisibilityChange() {
	if m == nil || m.modal == nil || m.modal.id != "agent-picker" {
		return
	}
	selectedID := m.agentPickerSelectedThreadID()
	previousIndex := m.modal.selected
	visible := m.visibleThreads()
	if len(visible) == 0 {
		m.modal = nil
		m.showAgentInfo("No agents available yet.")
		return
	}
	if selectedID != "" && !agentEntriesContainThread(visible, selectedID) {
		if previousIndex < 0 {
			previousIndex = 0
		}
		if previousIndex >= len(visible) {
			previousIndex = len(visible) - 1
		}
		selectedID = strings.TrimSpace(visible[previousIndex].ThreadID)
	}
	m.showAgentPicker(visible, currentAgentThreadID(m, ""), selectedID)
}

func (m *Model) removeAgentEntry(threadID string) {
	if m == nil {
		return
	}
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return
	}
	items := m.agentItems[:0]
	for _, entry := range m.agentItems {
		if strings.TrimSpace(entry.ThreadID) != threadID {
			items = append(items, entry)
		}
	}
	m.agentItems = items
	delete(m.archivedAgentThreads, threadID)
	delete(m.agentThreadArchiveCause, threadID)
	m.refreshAgentPickerAfterVisibilityChange()
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
