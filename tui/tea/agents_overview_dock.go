package tea

import (
	"math"
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

// agentsOverviewOwnsNestedInput reports whether a dashboard search, rename,
// help, removal or worktree flow owns keys (Rust view.owns_nested_input). A
// composer draft alone does not: the dashboard composer is the shared chat
// composer and the workspace focus keys still reach it.
func (m *Model) agentsOverviewOwnsNestedInput() bool {
	if m == nil || m.agentsOverview == nil {
		return false
	}
	state := m.agentsOverview.State
	return state.Searching || state.Renaming || state.Help ||
		state.CreatingWorktree || m.agentsOverviewLifecycleProgress != ""
}

// agentsOverviewBindingMatches reports whether an Agents action binds this key;
// those bindings take precedence over the workspace focus shortcuts (#62990).
func (m *Model) agentsOverviewBindingMatches(spec string) bool {
	if m == nil || spec == "" {
		return false
	}
	for _, action := range codextui.KeymapActions(codextui.KeymapActionFilter{}) {
		if action.Context != "agents" {
			continue
		}
		if m.keyMatches(action.Context, action.Action, spec) {
			return true
		}
	}
	return false
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
		m.agentsOverviewWorkspaceOpen() && !m.agentsOverviewOwnsNestedInput() {
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
		// Rust focus_workspace_composer leaves transcript interaction behind:
		// find, activity focus and a pending chord all return to the chat.
		m.clearMainTranscriptFind(false)
		m.activityFocus.active = false
		m.clearLeaderChord()
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
	// Rust focus_main_composer and focus_pane_right hand the workspace to the
	// conversation, but only while the dashboard owns no nested editor and no
	// Agents binding claims the key (#62990). Every other key stays with the
	// dashboard.
	if m.agentsOverviewWorkspaceOpen() && !m.agentsOverviewOwnsNestedInput() &&
		!m.agentsOverviewBindingMatches(spec) &&
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
	centerWidth, _, ok := m.agentsOverviewDockWidths()
	if !ok {
		return ""
	}
	// Rust workspace.motion slides the dock from its last painted width on
	// every frame. A detached requester (tests, embedders) or animations off
	// snaps to the settled width.
	fraction := m.advanceAgentsOverviewDockMotion(true, m.animationsEnabled && m.frames.attached())
	paintWidth := int(math.Round(float64(centerWidth) * fraction))
	conversationWidth := firstPositive(m.width, defaultWidth) - paintWidth - agentsOverviewDockGutter
	if conversationWidth < 1 {
		return ""
	}
	height := max(m.height, 1)
	m.syncAgentsOverviewRenderState()
	var center []string
	if paintWidth > 0 {
		// Rust paints the view into the fraction-width area, which is empty on
		// the first opening frame.
		center = m.agentsOverview.RenderStyled(paintWidth, height)
	}
	m.agentsOverviewDockFraction = fraction
	return m.zipAgentsOverviewDock(center, paintWidth, conversationWidth, height) +
		m.agentsOverviewCursorStyleSequence()
}

// renderClosingAgentsOverviewDock paints the shrinking dock after it closed.
// Rust keeps workspace.view alive and paints it while workspace.fraction
// decays, so the conversation grows back into the columns it releases.
func (m *Model) renderClosingAgentsOverviewDock() string {
	if m == nil || len(m.agentsOverviewClosingLines) == 0 || m.agentsOverview != nil {
		m.agentsOverviewClosingLines = nil
		return ""
	}
	if !m.animationsEnabled || !m.frames.attached() ||
		firstPositive(m.width, defaultWidth) < agentsOverviewDockMinSplit {
		m.agentsOverviewClosingLines = nil
		m.agentsOverviewClosingWidth = 0
		return ""
	}
	fraction := m.advanceAgentsOverviewDockMotion(false, true)
	if fraction <= 0 {
		m.agentsOverviewClosingLines = nil
		m.agentsOverviewClosingWidth = 0
		m.agentsOverviewDockFraction = 0
		return ""
	}
	settled := m.agentsOverviewClosingWidth
	if settled <= 0 {
		settled = agentsOverviewDockCenterWidth
	}
	paintWidth := int(math.Round(float64(settled) * fraction))
	conversationWidth := firstPositive(m.width, defaultWidth) - paintWidth - agentsOverviewDockGutter
	if conversationWidth < 1 {
		return ""
	}
	m.agentsOverviewDockFraction = fraction
	return m.zipAgentsOverviewDock(m.agentsOverviewClosingLines, paintWidth, conversationWidth, max(m.height, 1))
}

// zipAgentsOverviewDock pads the dock column, paints the live conversation at
// the remaining width and joins them around the dim divider.
func (m *Model) zipAgentsOverviewDock(center []string, centerWidth int, conversationWidth int, height int) string {
	conversation := m.agentsOverviewDockConversationLines(conversationWidth, height)
	// The conversation column sits right of the dock, so its pointer regions
	// move into frame coordinates like the side workspace does.
	m.offsetFocusedPanePointer(centerWidth+agentsOverviewDockGutter, 0)
	m.offsetSidePaneThreadLinks(centerWidth + agentsOverviewDockGutter)
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
	return strings.Join(rows, "\n")
}

// workspaceChatArea maps the terminal onto the conversation area: with the
// Command Center docked it starts right of the divider and keeps the column the
// dock left, exactly like Rust workspace_chat_area.
func (m *Model) workspaceChatArea() (x int, width int) {
	if m == nil {
		return 0, defaultWidth
	}
	width = firstPositive(m.width, defaultWidth)
	if !m.agentsOverviewDockOpen() {
		return 0, width
	}
	centerWidth, conversationWidth, ok := m.agentsOverviewDockWidths()
	if !ok {
		return 0, width
	}
	if fraction := m.agentsOverviewDockFraction; fraction > 0 && fraction < 1 {
		centerWidth = max(int(math.Round(float64(centerWidth)*fraction)), 1)
		conversationWidth = width - centerWidth - agentsOverviewDockGutter
		if conversationWidth < 1 {
			return 0, width
		}
	}
	return centerWidth + agentsOverviewDockGutter, conversationWidth
}

// agentsOverviewDockConversationLines paints the conversation column. Rust
// split_layout measures the side conversation against workspace_chat_area, so a
// side pane splits this column beside the dock instead of the whole terminal.
func (m *Model) agentsOverviewDockConversationLines(width int, height int) []string {
	m.agentsOverviewDockRendering = true
	defer func() { m.agentsOverviewDockRendering = false }()
	if width >= sideWorkspaceMinWidth && m.sideWorkspaceOpen() {
		if panes, ok := m.renderSideWorkspace(width, m.sideWorkspaceHeight()); ok {
			return strings.Split(panes, "\n")
		}
	}
	return m.renderFocusedPane(width, height)
}

// offsetSidePaneThreadLinks moves the watch-only reader's link columns into
// frame coordinates when the side conversation paints right of the dock.
func (m *Model) offsetSidePaneThreadLinks(x int) {
	if m == nil || x == 0 || len(m.sidePaneThreadLinks) == 0 {
		return
	}
	for row, links := range m.sidePaneThreadLinks {
		for index := range links {
			links[index].Start += x
			links[index].End += x
		}
		m.sidePaneThreadLinks[row] = links
	}
}

// snapshotClosingAgentsOverviewDock retains the settled dock column before the
// view closes so the closing slide can keep shrinking what was last painted.
func (m *Model) snapshotClosingAgentsOverviewDock() {
	if m == nil || m.agentsOverview == nil || !m.agentsOverviewDockOpen() {
		return
	}
	if !m.animationsEnabled || !m.frames.attached() {
		return
	}
	centerWidth, _, ok := m.agentsOverviewDockWidths()
	if !ok || centerWidth <= 0 {
		return
	}
	height := max(m.height, 1)
	m.syncAgentsOverviewRenderState()
	lines := m.agentsOverview.RenderStyled(centerWidth, height)
	if len(lines) == 0 {
		return
	}
	rows := make([]string, len(lines))
	for index := range lines {
		rows[index] = padSidePaneLine(codextui.TruncateToWidth(lines[index], centerWidth), centerWidth)
	}
	m.agentsOverviewClosingLines = rows
	m.agentsOverviewClosingWidth = centerWidth
}

// advanceAgentsOverviewDockMotion mirrors PaneMotion::advance for the docked
// Command Center.
func (m *Model) advanceAgentsOverviewDockMotion(open bool, animate bool) float64 {
	if m == nil {
		return 0
	}
	return m.advancePaneMotionState(&m.agentsOverviewDockMotion, open, animate)
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
	// Rust hit tests the layout it last painted, so a mid-slide frame uses the
	// animated dock width rather than the settled one.
	if fraction := m.agentsOverviewDockFraction; fraction > 0 && fraction < 1 {
		centerWidth = max(int(math.Round(float64(centerWidth)*fraction)), 1)
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
