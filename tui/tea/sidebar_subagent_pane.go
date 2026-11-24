package tea

import (
	"strings"

	bubbletea "charm.land/bubbletea/v2"

	"codex_go/tui/keymsg"
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
	if !m.sidebarAgentIsParentOwned(parent, threadID) {
		return nil, false
	}
	if tree := m.sidebarAgentTreeFor(parent, false); tree == nil || !tree.isMember(threadID) {
		// Discovery may not have reached the sidebar list yet, but the linked
		// agent's spawn ancestry already proves it belongs to this conversation.
		m.registerSidebarAgent(parent, threadID)
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
	if previous := m.retainedSideConversationForParent(open.ParentThreadID); watchOnlySidePane(previous) && previous.SideThreadID != open.ThreadID {
		// The reader shows one agent at a time; choosing another row replaces
		// the transcript instead of stacking tabs (Rust agents.detail).
		m.closeSidebarSubagentTranscript(previous)
	}
	m.retainSideConversation(side)
	m.activeSide = side
	m.sidebarReaderFocused = false
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
		m.sidebarReaderFocused = false
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

// focusSidebarReader gives the watch-only reader input focus so its transcript
// can be read with the keyboard (Rust #62157 pane focus).
func (m *Model) focusSidebarReader() bool {
	if m == nil || !watchOnlySidePane(m.activeSide) || m.activeSide.ShowingSide {
		return false
	}
	m.sidebarReaderFocused = true
	return true
}

// updateSidebarReaderKey scrolls the focused reader. Escape leaves it for the
// Subagents list, and every other key is consumed so the parent composer never
// receives input while the reader owns focus.
func (m *Model) updateSidebarReaderKey(msg keymsg.KeyMsg) (bubbletea.Cmd, bool) {
	if m == nil || !m.sidebarReaderFocused {
		return nil, false
	}
	if !watchOnlySidePane(m.activeSide) || m.activeSide.ShowingSide ||
		m.sidebarChooserVisible() || m.sidebarDocumentVisible() {
		// Another surface owns the sidebar now.
		m.sidebarReaderFocused = false
		return nil, false
	}
	page := max(m.transcript.Height-1, 1)
	switch msg.Type {
	case keymsg.KeyEsc:
		m.sidebarReaderFocused = false
		cmd, _ := m.escapeWatchOnlyReader()
		m.suppressCurrentViewKeyUntilRelease()
		return cmd, true
	case keymsg.KeyUp:
		return m.scrollSidebarReader(-1), true
	case keymsg.KeyDown:
		return m.scrollSidebarReader(1), true
	case keymsg.KeyPgUp:
		return m.scrollSidebarReader(-page), true
	case keymsg.KeyPgDown:
		return m.scrollSidebarReader(page), true
	}
	return nil, true
}

// scrollSidebarReader moves the reader's own transcript window.
func (m *Model) scrollSidebarReader(delta int) bubbletea.Cmd {
	side := m.activeSide
	if side == nil || delta == 0 {
		return nil
	}
	offset, saved := &side.SideScroll, &side.SideInteractionSaved
	if !*saved {
		*offset, *saved = side.SideViewMax, true
	}
	*offset = clampViewportOffset(*offset+delta, 0, side.SideViewMax)
	m.refreshTranscript()
	return nil
}

// watchOnlyReaderHeading is the dim top row Rust #62157 paints above the
// reader. Its back hint follows the resolved list Cancel binding so a custom
// keymap still advertises the key Escape uses.
func (m *Model) watchOnlyReaderHeading() string {
	back := ""
	if m != nil {
		back = strings.TrimSpace(m.sidebarChooserListHint("cancel"))
	}
	if back == "" {
		back = "esc"
	}
	return back + " back to agents · Watch only"
}

// escapeWatchOnlyReader mirrors Rust #62157's reader cancel action: Esc drops
// the reader (agents.detail = None) and returns to the Subagents list with the
// same agent still highlighted. The parent conversation keeps its composer.
func (m *Model) escapeWatchOnlyReader() (bubbletea.Cmd, bool) {
	if m == nil || m.State == nil || m.modal != nil || m.overlay != nil {
		return nil, false
	}
	side := m.activeSide
	if !watchOnlySidePane(side) || side.ShowingSide {
		return nil, false
	}
	if strings.TrimSpace(m.sidebarParentThreadID()) == "" {
		return nil, false
	}
	// The chooser reads the reader's own agent row before the tab disappears,
	// which is how the list comes back with the same selection.
	cmd := m.openSidebarChooser()
	m.closeSidebarSubagentTranscript(side)
	// Rust closes agents.detail without narrating it in the parent transcript.
	m.notice = ""
	m.refreshTranscript()
	return cmd, true
}

// sidebarAgentIsParentOwned reports whether threadID is a descendant of parent
// in the discovered spawn tree. A transcript link can name an agent the sidebar
// has not listed yet, so the agent list's spawn ancestry is the fallback
// (Rust #62159 backend ancestry confirmation).
func (m *Model) sidebarAgentIsParentOwned(parent string, threadID string) bool {
	parent = strings.TrimSpace(parent)
	threadID = strings.TrimSpace(threadID)
	if m == nil || parent == "" || threadID == "" || threadID == parent {
		return false
	}
	if tree := m.sidebarAgentTreeFor(parent, false); tree != nil && tree.isMember(threadID) {
		return true
	}
	seen := map[string]struct{}{}
	current := threadID
	for steps := 0; steps < 64; steps++ {
		if _, ok := seen[current]; ok {
			return false
		}
		seen[current] = struct{}{}
		entry, ok := m.agentEntry(current)
		if !ok {
			return false
		}
		next := strings.TrimSpace(entry.ParentThreadID)
		if next == "" {
			return false
		}
		if next == parent {
			return true
		}
		current = next
	}
	return false
}

// openAgentTranscript opens a thread picked from a transcript link or the
// Subagents list: a parent-owned agent lands in the sidebar reader, everything
// else keeps the ordinary conversation switch.
func (m *Model) openAgentTranscript(threadID string) bubbletea.Cmd {
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return nil
	}
	if cmd, ok := m.openSidebarSubagentTranscript(threadID); ok {
		return cmd
	}
	return m.applyAgentModalOptionAllowed(threadID, true)
}
