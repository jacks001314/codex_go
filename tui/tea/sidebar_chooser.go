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
	m.sidebarTabHover = -1
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
	m.sidebarTabHover = -1
	m.notice = ""
	m.refreshTranscript()
	return m.refreshStatusControlsCmd()
}

// sidebarChooserListActions are the shared `list` context actions the chooser
// honours, in tui/keymap.go order (Rust keymap.rs list bindings).
var sidebarChooserListActions = []string{
	"move_up", "move_down", "page_up", "page_down", "jump_top", "jump_bottom", "accept", "cancel",
}

// sidebarChooserVisibleRows is how many choice rows a chooser panel of the
// given height paints. Rust #59928 windows the list from the painted area
// (tui/src/sidebar_chooser.rs choice_rows): the tab strip, a spacer and the
// title sit above it and the footer hint takes the bottom row, so the list owns
// whatever is left.
func sidebarChooserVisibleRows(height int) int {
	if height <= 0 {
		return 0
	}
	if height <= 5 {
		return 1
	}
	return height - 5
}

// sidebarChooserPageRows is one page of chooser rows: the window the panel
// actually paints for its current height, never a fixed constant.
func (m *Model) sidebarChooserPageRows() int {
	if m == nil {
		return 1
	}
	return max(sidebarChooserVisibleRows(m.sidebarChooserPanelHeight()), 1)
}

// sidebarChooserPanelHeight is the pane height the chooser paints into;
// renderSideWorkspace hands renderSidebarChooser the transcript viewport.
func (m *Model) sidebarChooserPanelHeight() int {
	if m == nil {
		return 0
	}
	return firstPositive(m.transcript.Height, defaultHeight-defaultComposerHeight-2)
}

// sidebarChooserListAction resolves one key against the shared `list` bindings,
// so the chooser carries no private Up/Down/Esc/Enter list of its own.
func sidebarChooserListAction(msg keymsg.KeyMsg) string {
	// keymsg spells modifiers as "ctrl+n"/"alt+b" while tui/keymap.go lists them
	// as "ctrl-n"/"alt-b"; compare in the keymap's spelling.
	name := strings.NewReplacer("ctrl+", "ctrl-", "alt+", "alt-", "super+", "super-").Replace(msg.String())
	for _, action := range sidebarChooserListActions {
		binding, ok := codextui.FindKeymapAction("list", action)
		if !ok {
			continue
		}
		for _, candidate := range binding.DefaultBindings {
			if candidate == name {
				return action
			}
		}
	}
	return ""
}

// updateSidebarChooserKey handles the chooser's own keys while it owns input
// (Rust #59739 handle_sidebar_input, #59928 shared list bindings).
func (m *Model) updateSidebarChooserKey(msg keymsg.KeyMsg) (bool, bubbletea.Cmd) {
	if m == nil || !m.sidebarChooserVisible() || !m.sidebarChooserFocused {
		return false, nil
	}
	tabs := m.sidebarTabsFor(m.sidebarChooserParent)
	last := len(tabs) // the trailing "+ New side chat" row
	clamp := func(choice int) int {
		if choice < 0 {
			return 0
		}
		if choice > last {
			return last
		}
		return choice
	}
	switch sidebarChooserListAction(msg) {
	case "move_up":
		m.sidebarChoice = clamp(m.sidebarChoice - 1)
		return true, nil
	case "move_down":
		m.sidebarChoice = clamp(m.sidebarChoice + 1)
		return true, nil
	case "page_up":
		m.sidebarChoice = clamp(m.sidebarChoice - m.sidebarChooserPageRows())
		return true, nil
	case "page_down":
		m.sidebarChoice = clamp(m.sidebarChoice + m.sidebarChooserPageRows())
		return true, nil
	case "jump_top":
		m.sidebarChoice = 0
		return true, nil
	case "jump_bottom":
		m.sidebarChoice = last
		return true, nil
	case "accept":
		return true, m.openSidebarChoice(tabs)
	case "cancel":
		return true, m.closeSidebarChooser()
	}
	return false, nil
}

// sidebarTabStripVisible reports whether a tab strip is on screen: the chooser
// paints one as its first row, and a showing side chat paints one above the
// transcript (Rust #59784/#59928).
func (m *Model) sidebarTabStripVisible() bool {
	if m == nil || !m.sideWorkspaceEnabled() {
		return false
	}
	if m.sidebarChooserVisible() {
		return true
	}
	return m.activeSide != nil && m.activeSide.ShowingSide
}

// sidebarTabStripWindow is the strip's painted window: the cells
// renderSidebarTabStrip draws, reused by the hover and click hit tests so a
// pointer column always maps back to the tab painted there (Rust #59928
// sidebar_tab_strip.rs TabStrip::targets and hit share one layout).
type sidebarTabStripWindow struct {
	tabs     []*activeSideConversation
	labels   []string
	tabWidth int
	count    int
	first    int
	addStart int
	addWidth int
}

// sidebarTabStripWindowFor builds the window for a strip of the given width,
// keeping the selected tab in view (Rust #59784).
func sidebarTabStripWindowFor(tabs []*activeSideConversation, selected string, width int) sidebarTabStripWindow {
	labels := sidebarTabLabels(len(tabs))
	tabWidth, count := sidebarTabLayout(labels, width)
	first := 0
	selected = strings.TrimSpace(selected)
	if selected != "" {
		for index, tab := range tabs {
			if tab != nil && strings.TrimSpace(tab.SideThreadID) == selected {
				if count > 0 && index+1 > count {
					first = index + 1 - count
				}
				break
			}
		}
	}
	available, addWidth := sidebarTabStripSpace(width)
	return sidebarTabStripWindow{
		tabs:     tabs,
		labels:   labels,
		tabWidth: tabWidth,
		count:    count,
		first:    first,
		addStart: available,
		addWidth: addWidth,
	}
}

// tabAt maps a strip column back to the tab painted there, or -1 for the "+"
// button and for the blank cells after the last tab.
func (w sidebarTabStripWindow) tabAt(column int) int {
	if w.count == 0 || w.tabWidth <= 0 || column < 0 || column >= w.count*w.tabWidth {
		return -1
	}
	index := w.first + column/w.tabWidth
	if index < 0 || index >= len(w.tabs) {
		return -1
	}
	return index
}

// addAt reports whether the column lands on the trailing "+" button.
func (w sidebarTabStripWindow) addAt(column int) bool {
	return w.addStart >= 0 && column >= w.addStart && column < w.addStart+w.addWidth
}

// sidebarTabStripWindowWidth is the strip window the model paints at a width.
func (m *Model) sidebarTabStripWindowWidth(width int) sidebarTabStripWindow {
	if m == nil {
		return sidebarTabStripWindow{addStart: -1}
	}
	selected := ""
	if m.activeSide != nil {
		selected = strings.TrimSpace(m.activeSide.SideThreadID)
	}
	return sidebarTabStripWindowFor(m.sidebarTabsFor(m.sidebarParentThreadID()), selected, width)
}

// sidebarTabStripWidth is the width of the sidebar pane the strip paints in,
// which is what the strip's columns are measured against.
func (m *Model) sidebarTabStripWidth() int {
	width := firstPositive(m.width, defaultWidth)
	if _, right, _, ok := m.sidePaneLayout(width); ok && right > 0 {
		return right
	}
	return width
}

// sidebarTabStripWindow is the strip window the model currently paints.
func (m *Model) sidebarTabStripWindow() sidebarTabStripWindow {
	if m == nil {
		return sidebarTabStripWindow{addStart: -1}
	}
	return m.sidebarTabStripWindowWidth(m.sidebarTabStripWidth())
}

// hoverSidebarTab records which tab the pointer is over so the strip can paint
// it (Rust #59928 keeps the tabs hover-highlighted).
func (m *Model) hoverSidebarTab(column int) bool {
	if m == nil || !m.sidebarTabStripVisible() {
		return false
	}
	index := m.sidebarTabStripWindow().tabAt(column)
	if index == m.sidebarTabHover {
		return false
	}
	m.sidebarTabHover = index
	return true
}

// sidebarTabHoverIndex maps a bare strip column back to the tab painted there,
// or -1 when the column is the "+" button or past the last tab.
func sidebarTabHoverIndex(tabs []*activeSideConversation, column int, width int) int {
	return sidebarTabStripWindowFor(tabs, "", width).tabAt(column)
}

func sidebarTabLabels(tabCount int) []string {
	labels := make([]string, 0, tabCount)
	for index := 0; index < tabCount; index++ {
		if tabCount == 1 {
			labels = append(labels, sidebarSingleTabLabel)
			continue
		}
		labels = append(labels, " Side "+strconv.Itoa(index+1)+" ")
	}
	return labels
}

// sidebarTabStripSpace is the strip's horizontal budget, mirroring Rust
// sidebar_tab_strip.rs TabStrip::targets: the last column of the row stays
// blank, the add button reserves up to three more, and the tabs share the rest.
func sidebarTabStripSpace(width int) (int, int) {
	if width <= 0 {
		return 0, 0
	}
	inner := width - 1
	addWidth := min(inner, len(sidebarAddLabel))
	if addWidth < 0 {
		addWidth = 0
	}
	available := inner - addWidth
	if available < 0 {
		available = 0
	}
	return available, addWidth
}

// sidebarTabLayout returns the tab cell width and how many tabs fit in one
// strip row. Rust pins the cell to " Side chat " and clips a longer label at the
// cell edge instead of widening the cell, so the width never follows the labels.
func sidebarTabLayout(labels []string, width int) (int, int) {
	available, _ := sidebarTabStripSpace(width)
	tabWidth := sidebarTabWidth
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
	return tabWidth, count
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
	window := m.sidebarTabStripWindowWidth(width)
	var builder strings.Builder
	for offset := 0; offset < window.count; offset++ {
		index := window.first + offset
		if index >= len(window.labels) {
			break
		}
		cell := padSidebarCell(window.labels[index], window.tabWidth)
		if index == m.sidebarTabHover {
			cell = "\x1b[7m" + cell + "\x1b[27m"
		}
		builder.WriteString(cell)
	}
	used := window.count * window.tabWidth
	if remaining := window.addStart - used; remaining > 0 {
		builder.WriteString(strings.Repeat(" ", remaining))
	}
	builder.WriteString(padSidebarCell(sidebarAddLabel, window.addWidth))
	return codextui.TruncateWithEllipsis(builder.String(), width)
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
		// Rust #59928 sidebar_chooser.rs choice_rows: the list paints one
		// window of rows that keeps the selection in view, and the footer hint
		// keeps the last row. Page keys move by that same window.
		rows := len(tabs) + 1
		visible := rows
		if height > 0 {
			visible = min(rows, max(sidebarChooserVisibleRows(height), 1))
		}
		first := 0
		if m.sidebarChoice+1 > visible {
			first = m.sidebarChoice + 1 - visible
		}
		for index := first; index < rows && index < first+visible; index++ {
			marker := "  "
			if index == m.sidebarChoice {
				marker = "› "
			}
			if index == len(tabs) {
				lines = append(lines, marker+sidebarNewChatTitle)
				continue
			}
			title := "Side chat"
			if len(tabs) > 1 {
				title = "Side chat " + strconv.Itoa(index+1)
			}
			lines = append(lines, marker+"  "+title)
		}
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
