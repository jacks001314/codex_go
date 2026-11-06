package tea

import (
	"strconv"
	"strings"

	bubbletea "charm.land/bubbletea/v2"

	codextui "codex_go/tui"
	"codex_go/tui/keymsg"
)

// Rust #59739/#59767/#59784: each parent owns one sidebar. The chooser lists
// its sibling side chats plus a "New side chat" entry, and the tab strip stays
// on the sidebar's first row while a chat is showing.

const (
	sidebarSingleTabLabel = " Side chat "
	sidebarTabWidth       = len(sidebarSingleTabLabel)
	sidebarAddLabel       = " + "
	sidebarChooserTitle   = "Open in sidebar"
	sidebarNewChatTitle   = "+ New side chat"
)

// sidebarTabsFor returns the sibling side chats for a parent in creation order.
func (m *Model) sidebarTabsFor(parentThreadID string) []*activeSideConversation {
	if m == nil || len(m.sidebarTabs) == 0 {
		return nil
	}
	return m.sidebarTabs[strings.TrimSpace(parentThreadID)]
}

// registerSidebarTab records a side chat once, so the chooser can reopen it
// after another sibling takes focus.
func (m *Model) registerSidebarTab(side *activeSideConversation) {
	if m == nil || side == nil {
		return
	}
	parentThreadID := strings.TrimSpace(side.ParentThreadID)
	sideThreadID := strings.TrimSpace(side.SideThreadID)
	if parentThreadID == "" || sideThreadID == "" {
		return
	}
	if m.sidebarTabs == nil {
		m.sidebarTabs = map[string][]*activeSideConversation{}
	}
	for _, existing := range m.sidebarTabs[parentThreadID] {
		if existing != nil && strings.TrimSpace(existing.SideThreadID) == sideThreadID {
			return
		}
	}
	m.sidebarTabs[parentThreadID] = append(m.sidebarTabs[parentThreadID], side)
}

func (m *Model) unregisterSidebarTab(side *activeSideConversation) {
	if m == nil || side == nil || len(m.sidebarTabs) == 0 {
		return
	}
	parentThreadID := strings.TrimSpace(side.ParentThreadID)
	sideThreadID := strings.TrimSpace(side.SideThreadID)
	tabs := m.sidebarTabs[parentThreadID]
	if len(tabs) == 0 {
		return
	}
	kept := tabs[:0]
	for _, existing := range tabs {
		if existing == side || (existing != nil && strings.TrimSpace(existing.SideThreadID) == sideThreadID) {
			continue
		}
		kept = append(kept, existing)
	}
	if len(kept) == 0 {
		delete(m.sidebarTabs, parentThreadID)
		return
	}
	m.sidebarTabs[parentThreadID] = kept
}

// sidebarParentThreadID is the parent whose sidebar is on screen: the active
// side conversation's parent, otherwise the displayed thread.
func (m *Model) sidebarParentThreadID() string {
	if m == nil {
		return ""
	}
	if m.activeSide != nil {
		if parent := strings.TrimSpace(m.activeSide.ParentThreadID); parent != "" {
			return parent
		}
	}
	if m.State != nil {
		return strings.TrimSpace(m.State.ThreadID)
	}
	return ""
}

// sidebarChooserVisible reports whether the chooser panel is painted.
func (m *Model) sidebarChooserVisible() bool {
	if m == nil || !m.sidebarChooserOpen || !m.sideWorkspaceEnabled() {
		return false
	}
	return strings.TrimSpace(m.sidebarChooserParent) == m.sidebarParentThreadID()
}

// openSidebarChooser focuses the chooser on the parent conversation, returning
// from a side chat first so the list starts from the main thread.
func (m *Model) openSidebarChooser() bubbletea.Cmd {
	if m == nil || m.State == nil || !m.sideWorkspaceEnabled() {
		return nil
	}
	var cmd bubbletea.Cmd
	if m.activeSide != nil && m.activeSide.ShowingSide {
		cmd = m.toggleSideConversation()
	}
	parentThreadID := m.sidebarParentThreadID()
	if parentThreadID == "" {
		return cmd
	}
	m.sidebarChooserOpen = true
	m.sidebarChooserParent = parentThreadID
	m.sidebarChooserFocused = true
	tabs := m.sidebarTabsFor(parentThreadID)
	m.sidebarChoice = len(tabs)
	if m.activeSide != nil {
		selected := strings.TrimSpace(m.activeSide.SideThreadID)
		for index, tab := range tabs {
			if tab != nil && strings.TrimSpace(tab.SideThreadID) == selected {
				m.sidebarChoice = index
				break
			}
		}
	}
	m.notice = "Select a side chat or start a new one."
	m.refreshTranscript()
	if cmd != nil {
		return cmd
	}
	return m.refreshStatusControlsCmd()
}

func (m *Model) closeSidebarChooser() bubbletea.Cmd {
	if m == nil || !m.sidebarChooserOpen {
		return nil
	}
	m.sidebarChooserOpen = false
	m.sidebarChooserFocused = false
	m.sidebarChooserParent = ""
	m.sidebarChoice = 0
	m.notice = ""
	m.refreshTranscript()
	return m.refreshStatusControlsCmd()
}

// updateSidebarChooserKey handles the chooser's own keys while it owns input
// (Rust #59739 handle_sidebar_input).
func (m *Model) updateSidebarChooserKey(msg keymsg.KeyMsg) (bool, bubbletea.Cmd) {
	if m == nil || !m.sidebarChooserVisible() || !m.sidebarChooserFocused {
		return false, nil
	}
	tabs := m.sidebarTabsFor(m.sidebarChooserParent)
	switch msg.Type {
	case keymsg.KeyUp:
		if m.sidebarChoice > 0 {
			m.sidebarChoice--
		}
		return true, nil
	case keymsg.KeyDown:
		if m.sidebarChoice < len(tabs) {
			m.sidebarChoice++
		}
		return true, nil
	case keymsg.KeyEsc:
		return true, m.closeSidebarChooser()
	case keymsg.KeyEnter:
		return true, m.openSidebarChoice(tabs)
	}
	return false, nil
}

// openSidebarChoice reopens the selected side chat, or starts a new one from
// the parent chat when the "+ New side chat" row is selected.
func (m *Model) openSidebarChoice(tabs []*activeSideConversation) bubbletea.Cmd {
	if m == nil {
		return nil
	}
	if m.sidebarChoice >= 0 && m.sidebarChoice < len(tabs) {
		side := tabs[m.sidebarChoice]
		cmd := m.closeSidebarChooser()
		activate := m.activateRetainedSideConversation(side, "")
		return batchSideCommands(cmd, activate)
	}
	if m.sideStartPending {
		// Rust #59767: Enter retries after a failure and otherwise ignores the
		// repeated activation while the background fork is still running.
		return nil
	}
	cmd := m.closeSidebarChooser()
	m.sidebarChoice = 0
	start := m.startSideConversation("/side", "")
	return batchSideCommands(cmd, start)
}

func batchSideCommands(commands ...bubbletea.Cmd) bubbletea.Cmd {
	var kept []bubbletea.Cmd
	for _, command := range commands {
		if command != nil {
			kept = append(kept, command)
		}
	}
	switch len(kept) {
	case 0:
		return nil
	case 1:
		return kept[0]
	default:
		return bubbletea.Batch(kept...)
	}
}

// renderSidebarTabStrip draws the " Side chat " / " Side N " tabs plus the
// trailing " + " chooser button, keeping the selected tab visible when the
// pane is narrow (Rust #59784 sidebar_tab_strip.rs).
func (m *Model) renderSidebarTabStrip(width int) string {
	if m == nil || width <= 0 {
		return ""
	}
	tabs := m.sidebarTabsFor(m.sidebarParentThreadID())
	selected := ""
	if m.activeSide != nil {
		selected = strings.TrimSpace(m.activeSide.SideThreadID)
	}
	labels := make([]string, 0, len(tabs))
	for index := range tabs {
		if len(tabs) == 1 {
			labels = append(labels, sidebarSingleTabLabel)
			continue
		}
		labels = append(labels, " Side "+strconv.Itoa(index+1)+" ")
	}
	plus := sidebarAddLabel
	available := width - len(plus)
	if available < 0 {
		available = 0
	}
	tabWidth := sidebarTabWidth
	if len(labels) > 1 {
		tabWidth = countSidebarTabWidth(labels)
	}
	if tabWidth <= 0 {
		tabWidth = sidebarTabWidth
	}
	if tabWidth > available {
		tabWidth = available
	}
	count := 0
	if tabWidth > 0 {
		count = available / tabWidth
	}
	if count > len(labels) {
		count = len(labels)
	}
	first := 0
	for index, tab := range tabs {
		if tab != nil && strings.TrimSpace(tab.SideThreadID) == selected {
			if count > 0 && index+1 > count {
				first = index + 1 - count
			}
			break
		}
	}
	var builder strings.Builder
	for index := first; index < first+count && index < len(labels); index++ {
		builder.WriteString(padSidebarCell(labels[index], tabWidth))
	}
	used := count * tabWidth
	if remaining := width - used - len(plus); remaining > 0 {
		builder.WriteString(strings.Repeat(" ", remaining))
	}
	builder.WriteString(plus)
	return codextui.TruncateWithEllipsis(builder.String(), width)
}

func countSidebarTabWidth(labels []string) int {
	width := 0
	for _, label := range labels {
		if len(label) > width {
			width = len(label)
		}
	}
	return width
}

func padSidebarCell(label string, width int) string {
	if width <= 0 {
		return ""
	}
	if len(label) >= width {
		return label[:width]
	}
	return label + strings.Repeat(" ", width-len(label))
}

// renderSidebarChooser paints the chooser panel: the tab strip, the list of
// existing side chats, the new-chat row, and the footer hints.
func (m *Model) renderSidebarChooser(width int, height int) string {
	if m == nil || width <= 0 {
		return ""
	}
	tabs := m.sidebarTabsFor(m.sidebarChooserParent)
	lines := []string{m.renderSidebarTabStrip(width), ""}
	if m.sideStartPending {
		lines = append(lines, "Starting side conversation...")
		lines = append(lines, "")
		lines = append(lines, " Esc back ")
	} else {
		lines = append(lines, sidebarChooserTitle)
		for index := range tabs {
			marker := "  "
			if index == m.sidebarChoice {
				marker = "› "
			}
			title := "Side chat"
			if len(tabs) > 1 {
				title = "Side chat " + strconv.Itoa(index+1)
			}
			lines = append(lines, marker+"  "+title)
		}
		marker := "  "
		if m.sidebarChoice == len(tabs) {
			marker = "› "
		}
		lines = append(lines, marker+sidebarNewChatTitle)
		lines = append(lines, "")
		lines = append(lines, "Enter open · Esc back · ↑/↓ move")
	}
	if height > 0 && len(lines) > height {
		lines = lines[:height]
	}
	for index, line := range lines {
		lines[index] = codextui.TruncateWithEllipsis(line, width)
	}
	return strings.Join(lines, "\n")
}
