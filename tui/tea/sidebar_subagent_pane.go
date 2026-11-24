package tea

import (
	"strings"

	bubbletea "charm.land/bubbletea/v2"
)

// Rust parity: codex-rs/tui/src/app/sidebar_subagents.rs (#62157). Selecting a
// parent-owned subagent from the sidebar opens its transcript in a sidebar tab
// beside the parent conversation instead of replacing the main conversation.
// The pane is watch-only: it has no composer, so the agent cannot be messaged
// from it, and closing the tab never archives the agent's own thread.

// sidebarSubagentOpen records one in-flight transcript load started from the
// sidebar. The agent switch response arrives as AgentSwitchResultMsg.
type sidebarSubagentOpen struct {
	ParentThreadID string
	ThreadID       string
}

// openSidebarSubagentTranscript starts loading a parent-owned subagent's
// transcript. It reports false when the thread is not a member of the parent's
// spawn tree (a saved side chat or an unrelated thread keeps the ordinary
// switch path).
func (m *Model) openSidebarSubagentTranscript(threadID string) (bubbletea.Cmd, bool) {
	if m == nil || m.State == nil || !m.sideWorkspaceEnabled() {
		return nil, false
	}
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return nil, false
	}
	parent := strings.TrimSpace(m.sidebarParentThreadID())
	if parent == "" || threadID == parent {
		return nil, false
	}
	tree := m.sidebarAgentTreeFor(parent, false)
	if tree == nil || !tree.isMember(threadID) {
		return nil, false
	}
	switcher := m.onSwitchAgent
	if switcher == nil {
		return nil, false
	}
	open := &sidebarSubagentOpen{ParentThreadID: parent, ThreadID: threadID}
	m.pendingSidebarSubagent = open
	m.notice = "Opening " + m.sidebarSubagentLabel(parent, threadID) + "\u2026"
	return func() bubbletea.Msg {
		response, err := switcher(threadID)
		return AgentSwitchResultMsg{ThreadID: threadID, Response: response, Err: err}
	}, true
}

// applySidebarSubagentTranscript places a loaded subagent transcript in the
// side pane while the parent keeps the main pane and its composer. Rust
// restores the previous sidebar content when the agent cannot be shown.
func (m *Model) applySidebarSubagentTranscript(open *sidebarSubagentOpen, message AgentSwitchResultMsg) bubbletea.Cmd {
	if m == nil || open == nil {
		return nil
	}
	label := m.sidebarSubagentLabel(open.ParentThreadID, open.ThreadID)
	if message.Err != nil {
		text := strings.TrimSpace(message.Err.Error())
		if text == "" {
			text = "unknown error"
		}
		m.notice = "Could not open " + label + ": " + text
		return nil
	}
	side := &activeSideConversation{
		ParentThreadID:    open.ParentThreadID,
		SideThreadID:      open.ThreadID,
		ParentMessages:    cloneSideMessages(m.State.Messages),
		ParentStatus:      strings.TrimSpace(m.State.Status),
		ParentPlaceholder: m.composer.Placeholder,
		SideMessages:      cloneSideMessages(message.Response.Messages),
		SideStatus:        strings.TrimSpace(message.Response.Status),
		WatchOnly:         true,
		// The parent conversation owns the composer, so the passed-in agent
		// transcript is painted in the passive side pane.
		ShowingSide: false,
	}
	m.closeSidebarChooser()
	m.retainSideConversation(side)
	m.activeSide = side
	m.resetSidePaneViewState()
	m.notice = label + " transcript"
	m.refreshTranscript()
	return nil
}

// closeSidebarSubagentTranscript drops a watch-only tab. Unlike a side chat it
// owns no host conversation, so the close callback must not run: archiving the
// agent thread would hide the subagent the user is reviewing.
func (m *Model) closeSidebarSubagentTranscript(side *activeSideConversation) bubbletea.Cmd {
	if m == nil || side == nil {
		return nil
	}
	m.unregisterSidebarTab(side)
	parentThreadID := strings.TrimSpace(side.ParentThreadID)
	if stored := m.sideConversations[parentThreadID]; stored == side {
		delete(m.sideConversations, parentThreadID)
	}
	if m.activeSide == side {
		m.activeSide = nil
		m.resetSidePaneViewState()
	}
	m.notice = "Closed subagent transcript."
	m.refreshTranscript()
	return nil
}

// sidebarSubagentLabel names a spawn-tree row the way the chooser does.
func (m *Model) sidebarSubagentLabel(parent string, threadID string) string {
	threadID = strings.TrimSpace(threadID)
	tree := m.sidebarAgentTreeFor(parent, false)
	if tree != nil {
		if agent := tree.agents[threadID]; agent != nil {
			if name := strings.TrimSpace(agent.nickname); name != "" {
				if role := strings.TrimSpace(agent.role); role != "" {
					return name + " [" + role + "]"
				}
				return name
			}
			if path := strings.TrimSpace(agent.agentPath); path != "" {
				return path
			}
		}
	}
	return threadID
}

// watchOnlySidePane reports whether the side pane is a read-only subagent
// transcript (Rust #62157 has no composer or status line in this reader).
func watchOnlySidePane(side *activeSideConversation) bool {
	return side != nil && side.WatchOnly
}
