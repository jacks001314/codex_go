package tea

import (
	"strings"

	agentsoverview "codex_go/tui/agents_overview"
)

// threadReadReceipt is the observed revision captured when a read-only
// conversation is opened (Rust #58263 AppServerSession::frozen_receipt).
type threadReadReceipt struct {
	threadID string
	revision string
}

func (m *Model) applyAgentThreadReadState(message AgentThreadReadStateMsg) {
	if m == nil {
		return
	}
	m.rememberThreadReadState(message.ThreadID, message.Unread, message.Revision)
}

func (m *Model) rememberThreadReadState(threadID string, unread bool, revision string) {
	threadID = strings.TrimSpace(threadID)
	if m == nil || threadID == "" {
		return
	}
	revision = strings.TrimSpace(revision)
	if unread && revision != "" {
		if m.unreadThreadRevisions == nil {
			m.unreadThreadRevisions = map[string]string{}
		}
		m.unreadThreadRevisions[threadID] = revision
	} else {
		delete(m.unreadThreadRevisions, threadID)
	}
	if row, ok := m.agentsOverviewKnownRows[threadID]; ok {
		row.Unread = unread
		row.UnreadRevision = revision
		m.agentsOverviewKnownRows[threadID] = row
	}
	if m.agentsOverview == nil {
		return
	}
	for i := range m.agentsOverview.Rows {
		if strings.TrimSpace(m.agentsOverview.Rows[i].ThreadID) != threadID {
			continue
		}
		m.agentsOverview.Rows[i].Unread = unread
		m.agentsOverview.Rows[i].UnreadRevision = revision
	}
}

// rememberAgentsOverviewReadStates refreshes the receipt map from a dashboard
// listing. The rows retain the revision so a later focused view can acknowledge
// exactly the snapshot it observed.
func (m *Model) rememberAgentsOverviewReadStates(rows []agentsoverview.Row) {
	if m == nil {
		return
	}
	for _, row := range rows {
		threadID := strings.TrimSpace(row.ThreadID)
		if threadID == "" {
			continue
		}
		if row.Unread && strings.TrimSpace(row.UnreadRevision) != "" {
			if m.unreadThreadRevisions == nil {
				m.unreadThreadRevisions = map[string]string{}
			}
			m.unreadThreadRevisions[threadID] = strings.TrimSpace(row.UnreadRevision)
			continue
		}
		delete(m.unreadThreadRevisions, threadID)
	}
}

func (m *Model) freezeViewedReadReceipt(threadID string) {
	if m == nil {
		return
	}
	threadID = strings.TrimSpace(threadID)
	revision := strings.TrimSpace(m.unreadThreadRevisions[threadID])
	if threadID == "" || revision == "" {
		m.frozenReadReceipt = nil
		return
	}
	delete(m.unreadThreadRevisions, threadID)
	m.frozenReadReceipt = &threadReadReceipt{threadID: threadID, revision: revision}
}

func (m *Model) viewedThreadReadRevision(threadID string, frozen bool) string {
	if m == nil {
		return ""
	}
	threadID = strings.TrimSpace(threadID)
	var revision string
	if frozen {
		if m.frozenReadReceipt != nil && m.frozenReadReceipt.threadID == threadID {
			revision = m.frozenReadReceipt.revision
		}
	} else {
		revision = m.unreadThreadRevisions[threadID]
	}
	revision = strings.TrimSpace(revision)
	if revision == "" || revision == m.attemptedReadRevision {
		return ""
	}
	m.attemptedReadRevision = revision
	return revision
}

// visibleFocusedThreadID mirrors Rust's draw-time visibility gate: the
// conversation counts as viewed only while the terminal is focused and no
// modal, popup, dashboard, or non-transcript overlay obscures it.
func (m *Model) visibleFocusedThreadID() string {
	if m == nil || m.State == nil || !m.terminalFocused {
		return ""
	}
	if m.overlay != nil && !m.overlayTranscript {
		return ""
	}
	if m.agentsOverview != nil || (m.analyticsDashboard != nil && m.analyticsDashboard.active) {
		return ""
	}
	if m.modal != nil || m.slashPopup.Active || m.skillPopup.Active || m.mentionPopup != nil {
		return ""
	}
	if m.editorActive || m.windowsSandboxSetupActive || m.warningsView != nil {
		return ""
	}
	threadID := strings.TrimSpace(m.State.ThreadID)
	if threadID == "" || (m.threadUnavailable && !m.readOnlyThread) {
		return ""
	}
	return threadID
}

// observeFocusedThreadRead runs after a frame is requested. Opening a full view
// counts immediately; within the same view a newly arrived receipt is
// acknowledged once the agent turn is no longer running (Rust #58263).
func (m *Model) observeFocusedThreadRead() {
	if m == nil {
		return
	}
	visible := m.visibleFocusedThreadID()
	previous := m.focusedConversation
	if previous != visible {
		m.attemptedReadRevision = ""
	}
	m.focusedConversation = visible
	if visible == "" {
		return
	}
	if previous == visible && m.isTaskRunning() {
		return
	}
	revision := m.viewedThreadReadRevision(visible, m.readOnlyThread)
	if revision == "" || m.onAcknowledgeThreadRead == nil {
		return
	}
	m.onAcknowledgeThreadRead(visible, revision)
}
