package tea

import (
	"strings"

	bubbletea "github.com/charmbracelet/bubbletea"

	codextui "codex_go/tui"
)

// Rust #58720 groups threads with an outstanding approval first in /subagents,
// marks them with "(needs approval)", and selects the first waiting thread.
//
// The Go TUI keeps one approval modal at a time and has no per-thread event
// store, so a request addressed to an inactive thread waits in the delayed
// approval queue until that thread is selected. While it waits, the thread
// counts as pending for the picker and for the bottom-pane notice.

// routeApprovalRequest shows the request for the active thread and queues
// everything else: requests addressed to a background thread (Rust #58720) and
// requests that arrive while another approval modal is already visible.
func (m *Model) routeApprovalRequest(message ApprovalRequestMsg) bubbletea.Cmd {
	if m == nil {
		return nil
	}
	if owner, ok := m.requestBackendOwner(message.BackendID, message.ThreadID); ok && m.deferBackendRequest(owner, &message, nil) {
		return nil
	}
	threadID := strings.TrimSpace(message.ThreadID)
	if (threadID != "" && threadID != m.currentThreadID()) || (m.modal != nil && m.modal.kind == ModalKindApproval) {
		m.pendingApprovals = append(m.pendingApprovals, message)
		m.refreshThreadApprovalViews()
		return nil
	}
	return m.openApprovalModal(message)
}

// pendingApprovalThreadIDs reports the threads with an approval outstanding, in
// first-seen order: tracked picker rows first, then threads known only from a
// queued request.
func (m *Model) pendingApprovalThreadIDs() []string {
	if m == nil {
		return nil
	}
	active := m.currentThreadID()
	pending := map[string]bool{}
	if m.modal != nil && m.modal.kind == ModalKindApproval {
		if threadID := strings.TrimSpace(m.modal.threadID); threadID != "" && threadID != active {
			pending[threadID] = true
		}
	}
	for _, request := range m.pendingApprovals {
		threadID := strings.TrimSpace(request.ThreadID)
		if threadID != "" && threadID != active {
			pending[threadID] = true
		}
	}
	if len(pending) == 0 {
		return nil
	}
	out := make([]string, 0, len(pending))
	for _, entry := range m.agentItems {
		threadID := strings.TrimSpace(entry.ThreadID)
		if pending[threadID] {
			out = append(out, threadID)
			delete(pending, threadID)
		}
	}
	for _, request := range m.pendingApprovals {
		threadID := strings.TrimSpace(request.ThreadID)
		if pending[threadID] {
			out = append(out, threadID)
			delete(pending, threadID)
		}
	}
	if m.modal != nil && m.modal.kind == ModalKindApproval {
		threadID := strings.TrimSpace(m.modal.threadID)
		if pending[threadID] {
			out = append(out, threadID)
			delete(pending, threadID)
		}
	}
	return out
}

// pendingApprovalThreadSet reports the same threads as a set for row ordering.
func (m *Model) pendingApprovalThreadSet() map[string]bool {
	threadIDs := m.pendingApprovalThreadIDs()
	if len(threadIDs) == 0 {
		return nil
	}
	set := make(map[string]bool, len(threadIDs))
	for _, threadID := range threadIDs {
		set[threadID] = true
	}
	return set
}

// pendingThreadApprovalLabels renders the bottom-pane notice rows (Rust's
// pending_thread_approvals widget): one label per waiting thread.
func (m *Model) pendingThreadApprovalLabels() []string {
	threadIDs := m.pendingApprovalThreadIDs()
	if len(threadIDs) == 0 {
		return nil
	}
	labels := make([]string, 0, len(threadIDs))
	for _, threadID := range threadIDs {
		labels = append(labels, m.threadLabel(threadID))
	}
	return labels
}

// threadLabel is Rust's thread_routing::thread_label: the agent path wins for
// non-primary threads, anonymous agents are disambiguated by the tail of their
// UUID, and the main thread keeps the "Main [default]" name.
func (m *Model) threadLabel(threadID string) string {
	threadID = strings.TrimSpace(threadID)
	if entry, ok := m.agentEntry(threadID); ok {
		return agentEntryThreadLabel(entry)
	}
	if m != nil && m.isPrimaryThread(threadID) {
		return "Main [default]"
	}
	return "Agent (" + agentThreadIDSuffix(threadID) + ")"
}

func (m *Model) isPrimaryThread(threadID string) bool {
	threadID = strings.TrimSpace(threadID)
	if m == nil || threadID == "" {
		return false
	}
	if entry, ok := m.agentEntry(threadID); ok {
		return entry.IsPrimary
	}
	return false
}

// agentThreadIDSuffix returns the UUID tail that distinguishes agents spawned
// in the same batch: they share the timestamp prefix of their UUIDv7 ids.
func agentThreadIDSuffix(threadID string) string {
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return ""
	}
	if index := strings.LastIndex(threadID, "-"); index >= 0 && index+1 < len(threadID) {
		return threadID[index+1:]
	}
	return threadID
}

// agentEntryThreadLabel applies the same naming rules to a picker entry.
func agentEntryThreadLabel(entry codextui.AgentThreadEntry) string {
	if !entry.IsPrimary {
		if path := strings.TrimSpace(entry.AgentPath); path != "" {
			return path
		}
	}
	label := strings.TrimSpace(entry.DisplayLabel())
	if label == "" {
		label = "Agent"
	}
	if entry.IsPrimary {
		return label
	}
	if strings.TrimSpace(entry.AgentNickname) == "" {
		return label + " (" + agentThreadIDSuffix(entry.ThreadID) + ")"
	}
	return label
}

// openPendingApprovalForThread replays the oldest queued request for threadID
// once it becomes the active thread (Rust's switch-time replay).
func (m *Model) openPendingApprovalForThread(threadID string) bubbletea.Cmd {
	if m == nil {
		return nil
	}
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return nil
	}
	if m.modal != nil && m.modal.kind == ModalKindApproval && strings.TrimSpace(m.modal.threadID) == threadID {
		return nil
	}
	request, ok := m.dequeueApproval(func(queued string) bool { return queued == threadID })
	if !ok {
		return nil
	}
	return m.openApprovalModal(request)
}

// openNextThreadApproval surfaces the oldest request that belongs to the active
// thread once a modal has been answered.
func (m *Model) openNextThreadApproval() bubbletea.Cmd {
	if m == nil {
		return nil
	}
	if m.modal != nil && m.modal.kind == ModalKindApproval {
		return nil
	}
	active := m.currentThreadID()
	request, ok := m.dequeueApproval(func(queued string) bool {
		return queued == "" || queued == active
	})
	if !ok {
		return nil
	}
	return m.openApprovalModal(request)
}

// dequeueApproval removes the first queued request whose thread matches.
func (m *Model) dequeueApproval(match func(threadID string) bool) (ApprovalRequestMsg, bool) {
	if m == nil || match == nil {
		return ApprovalRequestMsg{}, false
	}
	for index, request := range m.pendingApprovals {
		if !match(strings.TrimSpace(request.ThreadID)) {
			continue
		}
		m.pendingApprovals = append(m.pendingApprovals[:index], m.pendingApprovals[index+1:]...)
		return request, true
	}
	return ApprovalRequestMsg{}, false
}

// removeQueuedApproval forgets a queued request that the app-server resolved
// through another path.
func (m *Model) removeQueuedApproval(requestID string) bool {
	requestID = strings.TrimSpace(requestID)
	if m == nil || requestID == "" {
		return false
	}
	for index, request := range m.pendingApprovals {
		if strings.TrimSpace(request.ServerRequestID) != requestID && strings.TrimSpace(request.ID) != requestID {
			continue
		}
		m.pendingApprovals = append(m.pendingApprovals[:index], m.pendingApprovals[index+1:]...)
		return true
	}
	return false
}

// refreshThreadApprovalViews re-renders an open picker after the pending set
// changed, keeping the selected thread (Rust refresh_agent_picker).
func (m *Model) refreshThreadApprovalViews() {
	if m == nil || m.modal == nil || m.modal.id != "agent-picker" {
		return
	}
	m.rebuildAgentPicker()
}

// dropStaleThreadApprovals forgets queued requests after the app-server
// connection goes away: their brokers belong to the retired client, so no
// answer could be delivered. Rust re-derives the pending set from the
// per-thread stores when it reconnects (#58720).
func (m *Model) dropStaleThreadApprovals() {
	if m == nil || len(m.pendingApprovals) == 0 {
		return
	}
	m.pendingApprovals = nil
	m.refreshThreadApprovalViews()
}
