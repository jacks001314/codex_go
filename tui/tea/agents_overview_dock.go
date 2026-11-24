package tea

import (
	"strings"

	bubbletea "charm.land/bubbletea/v2"

	codextui "codex_go/tui"
	"codex_go/tui/keymsg"
	mousemsg "codex_go/tui/mousemsg"
)

// Rust parity: codex-rs/tui/src/app/conversation_workspace.rs (#62986). With
// the TUI workspace enabled, the Agent Command Center docks to the left of the
// conversation on wide terminals so the transcript and draft stay visible. On
// narrow terminals the focused pane fills the screen, which is the dashboard's
// existing full-screen path.

const (
	agentsOverviewDockCenterWidth = 36
	agentsOverviewDockGutter      = 2
	agentsOverviewDockMinChat     = 48
	agentsOverviewDockMinSplit    = agentsOverviewDockCenterWidth + agentsOverviewDockGutter + agentsOverviewDockMinChat

	agentsOverviewDockCenter       = "center"
	agentsOverviewDockConversation = "conversation"
)

// agentsOverviewDockOpen reports whether the Command Center paints beside the
// conversation: it must be open and the terminal must have room for both
// panes, exactly like Rust's workspace_areas MIN_SPLIT_WIDTH check.
func (m *Model) agentsOverviewDockOpen() bool {
	return m.agentsOverviewWorkspaceOpen() &&
		firstPositive(m.width, defaultWidth) >= agentsOverviewDockMinSplit
}

// agentsOverviewWorkspaceOpen reports whether the Command Center is the
// workspace pane Rust #62986 docks: it needs the owned transcript renderer and
// the workspace feature, but not room for both columns. A narrow terminal
// paints whichever pane owns input full screen.
func (m *Model) agentsOverviewWorkspaceOpen() bool {
	if m == nil || m.agentsOverview == nil || !m.sideWorkspaceEnabled() || !m.ownedScreen {
		return false
	}
	return true
}

// agentsOverviewCenterOwnsInput reports whether the dashboard, rather than the
// conversation, receives keys. The dock opens on the center, and only an
// explicit focus move hands input to the conversation.
func (m *Model) agentsOverviewCenterOwnsInput() bool {
	return m != nil && m.agentsOverview != nil && !m.agentsOverviewDockConversationFocused()
}

// handleAgentsOverviewWorkspaceChord routes leader chords while the dashboard
// owns input. Rust handle_workspace_key sees the routed chord completion first
// and turns focus_pane_right into a workspace focus move; every other chord
// keeps its normal action.
func (m *Model) handleAgentsOverviewWorkspaceChord(spec string) (bool, bubbletea.Cmd) {
	if m == nil || spec == "" {
		return false, nil
	}
	if m.leaderChordPending && m.leaderChordActions[spec] == "global.focus_pane_right" &&
		m.agentsOverviewWorkspaceOpen() {
		m.clearLeaderChord()
		m.focusAgentsOverviewDockConversation()
		return true, nil
	}
	return m.handleLeaderChordKey(spec, nil)
}

// agentsOverviewNarrowConversationFillsScreen reports whether the workspace
// has no room for both panes while the conversation owns input; Rust then
// paints that pane across the whole viewport.
func (m *Model) agentsOverviewNarrowConversationFillsScreen() bool {
	return m != nil && m.agentsOverview != nil && !m.agentsOverviewDockRendering &&
		!m.agentsOverviewDockOpen() && m.agentsOverviewDockConversationFocused()
}

func (m *Model) agentsOverviewDockConversationFocused() bool {
	return m != nil && m.agentsOverviewDockFocus == agentsOverviewDockConversation
}

// focusAgentsOverviewDockConversation hands input to the conversation while
// keeping the dock painted (Rust focus_workspace_composer).
func (m *Model) focusAgentsOverviewDockConversation() bool {
	if m == nil || !m.agentsOverviewWorkspaceOpen() || !m.agentsOverviewCanReturnToChat() {
		return false
	}
	if m.agentsOverviewDockFocus != agentsOverviewDockConversation {
		m.agentsOverviewDockFocus = agentsOverviewDockConversation
		m.rearmActivationKeyGuard()
		m.refreshTranscript()
	}
	return true
}

// focusAgentsOverviewDockCenter gives the dock its own input back, used by the
// focus_pane_left and open_agents shortcuts while the conversation is focused.
func (m *Model) focusAgentsOverviewDockCenter() bool {
	if m == nil || !m.agentsOverviewWorkspaceOpen() {
		return false
	}
	m.agentsOverviewDockFocus = agentsOverviewDockCenter
	return true
}

// keepDockedAgentsOverviewAfterOpen leaves the dock open after a task opens and
// hands the conversation its input, matching Rust #62986.
func (m *Model) keepDockedAgentsOverviewAfterOpen() bool {
	if m == nil || !m.agentsOverviewWorkspaceOpen() {
		return false
	}
	m.agentsOverviewDockFocus = agentsOverviewDockConversation
	return true
}

// handleDockedAgentsOverviewKey routes keys while the dock is open. The center
// keeps the dashboard's own handling; focus_main_composer or focus_pane_right
// moves input to the conversation, and focus_pane_left or open_agents moves it
// back.
func (m *Model) handleDockedAgentsOverviewKey(msg keymsg.KeyMsg) (bool, bubbletea.Cmd) {
	if m == nil || m.agentsOverview == nil {
		return false, nil
	}
	spec := keySpecFromKeyMsg(msg)
	if m.agentsOverviewDockConversationFocused() {
		if m.keyMatches("global", "focus_pane_left", spec) || m.keyMatches("global", "open_agents", spec) {
			m.focusAgentsOverviewDockCenter()
			return true, nil
		}
		return false, nil
	}
	// Rust focus_main_composer and focus_pane_right both hand the workspace
	// to the conversation, and every other key stays with the dashboard.
	if m.agentsOverviewWorkspaceOpen() &&
		(m.keyMatches("global", "focus_main_composer", spec) ||
			m.keyMatches("global", "focus_pane_right", spec)) {
		m.focusAgentsOverviewDockConversation()
		return true, nil
	}
	return false, nil
}

// agentsOverviewDockWidths splits the workspace into the dock and the
// conversation, mirroring Rust workspace_areas: a fixed 36-column dock capped
// so the conversation keeps at least 48 columns, then a two-column gutter.
func (m *Model) agentsOverviewDockWidths() (centerWidth int, conversationWidth int, ok bool) {
	width := firstPositive(m.width, defaultWidth)
	if width < agentsOverviewDockMinSplit {
		return 0, 0, false
	}
	centerWidth = agentsOverviewDockCenterWidth
	if m.agentsOverviewDockWidth > 0 {
		centerWidth = m.agentsOverviewDockWidth
	}
	centerWidth = min(centerWidth, width-agentsOverviewDockMinChat-agentsOverviewDockGutter)
	centerWidth = max(centerWidth, 24)
	conversationWidth = width - centerWidth - agentsOverviewDockGutter
	if conversationWidth < 1 {
		return 0, 0, false
	}
	return centerWidth, conversationWidth, true
}

// renderAgentsOverviewDock paints the docked Command Center beside the live
// conversation, or returns empty so the caller keeps the full-screen dashboard.
func (m *Model) renderAgentsOverviewDock() string {
	if m == nil || m.agentsOverview == nil || !m.agentsOverviewDockOpen() {
		return ""
	}
	centerWidth, conversationWidth, ok := m.agentsOverviewDockWidths()
	if !ok {
		return ""
	}
	height := max(m.height, 1)
	m.syncAgentsOverviewRenderState()
	center := m.agentsOverview.RenderStyled(centerWidth, height)
	if len(center) == 0 {
		return ""
	}
	m.agentsOverviewDockRendering = true
	conversation := m.renderFocusedPane(conversationWidth, height)
	m.agentsOverviewDockRendering = false
	// The conversation column sits right of the dock, so its pointer regions
	// move into frame coordinates like the side workspace does.
	m.offsetFocusedPanePointer(centerWidth+agentsOverviewDockGutter, 0)
	divider := codextui.TruncateToWidth("\x1b[2m│\x1b[0m ", agentsOverviewDockGutter)
	rows := make([]string, 0, height)
	for index := 0; index < height; index++ {
		left := ""
		if index < len(center) {
			left = center[index]
		}
		left = padSidePaneLine(codextui.TruncateToWidth(left, centerWidth), centerWidth)
		right := ""
		if index < len(conversation) {
			right = conversation[index]
		}
		rows = append(rows, left+divider+right)
	}
	return strings.Join(rows, "\n") + m.agentsOverviewCursorStyleSequence()
}

// handleAgentsOverviewDockMouse routes presses in the docked workspace. A
// press over the dock focuses it and keeps the dashboard's row handling; a
// press on the divider starts a resize; presses on the conversation column
// fall through to the transcript after handing it focus (Rust #62986).
func (m *Model) handleAgentsOverviewDockMouse(msg mousemsg.MouseMsg) (bubbletea.Cmd, bool) {
	if m == nil || m.agentsOverview == nil || !m.agentsOverviewDockOpen() {
		return nil, false
	}
	centerWidth, _, ok := m.agentsOverviewDockWidths()
	if !ok {
		return nil, false
	}
	if msg.Action == mousemsg.MouseActionPress && msg.Button == mousemsg.MouseButtonLeft && msg.X == centerWidth {
		m.agentsOverviewDockDragging = true
		return nil, true
	}
	if m.agentsOverviewDockDragging {
		switch msg.Action {
		case mousemsg.MouseActionMotion, mousemsg.MouseActionRelease:
			m.resizeAgentsOverviewDock(msg.X)
			if msg.Action == mousemsg.MouseActionRelease {
				m.agentsOverviewDockDragging = false
			}
			return nil, true
		}
	}
	if msg.X < centerWidth {
		if msg.Action == mousemsg.MouseActionPress && msg.Button == mousemsg.MouseButtonLeft {
			m.agentsOverviewDockFocus = agentsOverviewDockCenter
			if cmd, handled := m.updateAgentsOverviewMouse(msg); handled {
				return cmd, true
			}
			m.refreshTranscript()
		}
		return nil, true
	}
	if msg.X < centerWidth+agentsOverviewDockGutter {
		return nil, true
	}
	if msg.Action == mousemsg.MouseActionPress && msg.Button == mousemsg.MouseButtonLeft {
		if !m.agentsOverviewDockConversationFocused() {
			m.focusAgentsOverviewDockConversation()
		}
	}
	return nil, false
}

// resizeAgentsOverviewDock clamps a dragged divider column the way Rust's
// workspace mouse handler does: at least 24 dock columns and a 48-column
// conversation plus the gutter.
func (m *Model) resizeAgentsOverviewDock(column int) {
	if m == nil {
		return
	}
	maxWidth := firstPositive(m.width, defaultWidth) - agentsOverviewDockMinChat - agentsOverviewDockGutter
	if maxWidth < 24 {
		maxWidth = 24
	}
	m.agentsOverviewDockWidth = clampModalInt(column, 24, maxWidth)
	m.refreshTranscript()
}
