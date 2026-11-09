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
	// Rust #60319: adjacent tabs paint one noninteractive column between them.
	sidebarTabGap       = 1
	sidebarAddLabel     = " + "
	sidebarChooserTitle = "Open in sidebar"
	sidebarNewChatTitle = "+ New side chat"

	// Rust #60319 sidebar_chooser.rs: the new-chat row explains where a side
	// chat starts, the chooser waits for the first main-chat turn, and a failed
	// discovery offers an inline retry row.
	sidebarNewChatSubtitle     = "Start from the main chat"
	sidebarWaitingSideChats    = "You can start a side chat after your first message in the main chat."
	sidebarRetryingChatsTitle  = "Retrying…"
	sidebarRetryChatsTitle     = "Retry loading side chats"
	sidebarSideChatsGroupTitle = "Side chats"
)

// SideChatChoice is one saved side chat the chooser can reopen (Rust #60319
// SideChat): an optional title and the starting prompt used as a preview.
type SideChatChoice struct {
	ThreadID string
	Name     string
	Preview  string
}

// SideChatDiscoveryKind mirrors Rust's sidebar_chooser::Discovery.
type SideChatDiscoveryKind string

const (
	SideChatDiscoveryIdle    SideChatDiscoveryKind = "idle"
	SideChatDiscoveryLoading SideChatDiscoveryKind = "loading"
	SideChatDiscoveryFailed  SideChatDiscoveryKind = "failed"
)

// SideChatListFunc discovers the saved side chats attached to a parent.
type SideChatListFunc func(parentThreadID string) ([]SideChatChoice, error)

// SideChatPromptFunc reads the latest user prompt for the visible chats.
type SideChatPromptFunc func(threadIDs []string) map[string]string

// SideOpenFunc attaches a saved side chat so the sidebar can show it.
type SideOpenFunc func(parentThreadID, sideThreadID string) (AgentThreadSwitchResponse, error)

type SideChatsLoadedMsg struct {
	ParentThreadID string
	RequestID      uint64
	Choices        []SideChatChoice
	Err            error
}

type SideChatPromptsLoadedMsg struct {
	ParentThreadID string
	RequestID      uint64
	Prompts        map[string]string
}

type SideOpenResultMsg struct {
	ParentThreadID string
	SideThreadID   string
	Response       AgentThreadSwitchResponse
	Err            error
}

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
	m.sidebarAddHover = false
	// Rust #60319 App::show(Chooser): the selection starts on the chat that is
	// already showing, falling back to the new-chat row.
	m.sidebarChoice = 0
	selected := ""
	if m.activeSide != nil && strings.TrimSpace(m.activeSide.ParentThreadID) == parentThreadID {
		selected = strings.TrimSpace(m.activeSide.SideThreadID)
	}
	if index := m.sidebarChooserChoiceIndex(selected); index > 0 {
		m.sidebarChoice = index
	}
	m.notice = "Select a side chat or start a new one."
	m.refreshTranscript()
	discovery := m.requestSideChatDiscovery(false)
	if discovery == nil {
		discovery = m.refreshStatusControlsCmd()
	}
	return batchSideCommands(cmd, discovery)
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
	m.sidebarAddHover = false
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
	rows := len(m.sidebarChooserWindow(m.sidebarChooserPanelHeight()))
	if rows < 1 {
		return 1
	}
	return rows
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
// (Rust #59739 handle_sidebar_input, #59928 shared list bindings, #60319
// discovery waiting state).
func (m *Model) updateSidebarChooserKey(msg keymsg.KeyMsg) (bool, bubbletea.Cmd) {
	if m == nil || !m.sidebarChooserVisible() || !m.sidebarChooserFocused {
		return false, nil
	}
	action := sidebarChooserListAction(msg)
	if action == "" {
		return false, nil
	}
	if !m.sidebarChatCreationAvailable() && action != "cancel" {
		// Rust #60319: before the first main-chat turn the chooser explains the
		// wait and consumes every list action except Cancel.
		return true, nil
	}
	last := m.sidebarChooserChoiceCount() - 1
	clamp := func(choice int) int {
		if choice < 0 {
			return 0
		}
		if choice > last {
			return max(last, 0)
		}
		return choice
	}
	switch action {
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
		m.sidebarChoice = clamp(last)
		return true, nil
	case "accept":
		// Rust #60319 guard_chooser_key: a held activation key must not reach
		// the view the chooser just opened.
		m.armSelectExitGuard(keySpecFromKeyMsg(msg))
		return true, m.acceptSidebarChooser()
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
	stride   int
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
		stride:   tabWidth + sidebarTabGap,
		count:    count,
		first:    first,
		addStart: available,
		addWidth: addWidth,
	}
}

// tabAt maps a strip column back to the tab painted there, or -1 for the "+"
// button and for the blank cells after the last tab.
func (w sidebarTabStripWindow) tabAt(column int) int {
	if w.count == 0 || w.tabWidth <= 0 || w.stride <= 0 || column < 0 || column >= w.count*w.stride {
		return -1
	}
	offset := column / w.stride
	if column-offset*w.stride >= w.tabWidth {
		// The gap between tabs is not an interactive target.
		return -1
	}
	index := w.first + offset
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

// hoverSidebarTab records which strip target the pointer is over so the strip
// can paint it (Rust #59928 keeps the tabs hover-highlighted, #60319 also
// highlights the add button and excludes the gaps between tabs).
func (m *Model) hoverSidebarTab(column int) bool {
	if m == nil || !m.sidebarTabStripVisible() {
		return false
	}
	window := m.sidebarTabStripWindow()
	index := window.tabAt(column)
	add := window.addAt(column)
	if index == m.sidebarTabHover && add == m.sidebarAddHover {
		return false
	}
	m.sidebarTabHover = index
	m.sidebarAddHover = add
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
		// Each tab after the first also needs its separating gap column.
		count = (available + sidebarTabGap) / (tabWidth + sidebarTabGap)
	}
	if count > len(labels) {
		count = len(labels)
	}
	return tabWidth, count
}

// acceptSidebarChooser opens the row the key was accepted on: the new-chat
// row, a saved chat, or the discovery retry row (Rust #60319 choice semantics:
// zero is New side chat, one through N are the saved chats).
func (m *Model) acceptSidebarChooser() bubbletea.Cmd {
	if m == nil {
		return nil
	}
	choices := m.sidebarChooserChoices()
	switch {
	case m.sidebarChoice == 0:
		if !m.sidebarChatCreationAvailable() {
			return nil
		}
		cmd := m.closeSidebarChooser()
		m.sidebarChoice = 0
		return batchSideCommands(cmd, m.startSideConversation("/side", ""))
	case m.sidebarChooserIsRetryRow(m.sidebarChoice, choices):
		return m.requestSideChatDiscovery(true)
	default:
		choice, ok := m.sidebarChooserChat(m.sidebarChoice)
		if !ok {
			return nil
		}
		if side := m.liveSideTab(choice.ThreadID); side != nil {
			return batchSideCommands(m.closeSidebarChooser(), m.activateRetainedSideConversation(side, ""))
		}
		if m.onOpenSideChat == nil {
			return nil
		}
		parentThreadID := m.sidebarChooserParent
		sideThreadID := choice.ThreadID
		open := m.onOpenSideChat
		return func() bubbletea.Msg {
			response, err := open(parentThreadID, sideThreadID)
			return SideOpenResultMsg{ParentThreadID: parentThreadID, SideThreadID: sideThreadID, Response: response, Err: err}
		}
	}
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
		if offset > 0 {
			// Rust #60319 TAB_GAP: one blank, noninteractive column.
			builder.WriteString(strings.Repeat(" ", sidebarTabGap))
		}
		cell := padSidebarCell(window.labels[index], window.tabWidth)
		if index == m.sidebarTabHover {
			cell = "\x1b[7m" + cell + "\x1b[27m"
		}
		builder.WriteString(cell)
	}
	used := 0
	if window.count > 0 {
		used = window.count*window.tabWidth + (window.count-1)*sidebarTabGap
	}
	if remaining := window.addStart - used; remaining > 0 {
		builder.WriteString(strings.Repeat(" ", remaining))
	}
	addCell := padSidebarCell(sidebarAddLabel, window.addWidth)
	if m.sidebarAddHover {
		addCell = "\x1b[7m" + addCell + "\x1b[27m"
	}
	builder.WriteString(addCell)
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

// renderSidebarChooser paints the chooser panel: the tab strip, discovery
// status, the new-chat row, the saved chats with their latest prompt previews,
// an inline retry row after a discovery failure, and the footer hints.
func (m *Model) renderSidebarChooser(width int, height int) string {
	if m == nil || width <= 0 {
		return ""
	}
	if m.sideStartPending {
		lines := []string{m.renderSidebarTabStrip(width), "", "Starting side conversation...", "", " Esc back "}
		if height > 0 && len(lines) > height {
			lines = lines[:height]
		}
		for index, line := range lines {
			lines[index] = codextui.TruncateWithEllipsis(line, width)
		}
		return strings.Join(lines, "\n")
	}
	lines := []string{m.renderSidebarTabStrip(width), "", sidebarChooserTitle}
	switch m.sidebarDiscovery {
	case SideChatDiscoveryLoading:
		if len(m.sidebarChooserChoices()) == 0 {
			lines = append(lines, "… Loading side chats…")
		} else {
			lines = append(lines, "… Refreshing side chats…")
		}
	case SideChatDiscoveryFailed:
		lines = append(lines, "\x1b[31m"+m.sidebarDiscoveryError+"\x1b[39m")
	default:
		lines = append(lines, "")
	}
	if !m.sidebarChatCreationAvailable() {
		lines = append(lines, sidebarWaitingSideChats, "", "Esc back")
		if height > 0 && len(lines) > height {
			lines = lines[:height]
		}
		for index, line := range lines {
			lines[index] = codextui.TruncateWithEllipsis(line, width)
		}
		return strings.Join(lines, "\n")
	}
	choices := m.sidebarChooserChoices()
	groupHeader := len(choices) > 1
	body := make([]string, 0, len(choices)*2)
	for _, index := range m.sidebarChooserWindow(height) {
		marker := "  "
		if index == m.sidebarChoice {
			marker = "› "
		}
		switch {
		case index == 0:
			body = append(body, marker+sidebarNewChatTitle)
			if width >= 58 {
				body[len(body)-1] = marker + sidebarNewChatTitle + "  " + sidebarNewChatSubtitle
			}
		case m.sidebarChooserIsRetryRow(index, choices):
			label := sidebarRetryChatsTitle
			if m.sidebarDiscovery == SideChatDiscoveryLoading {
				label = sidebarRetryingChatsTitle
			}
			body = append(body, marker+"  "+label)
		default:
			choice, _ := m.sidebarChooserChat(index)
			if groupHeader && index == 1 {
				body = append(body, sidebarSideChatsGroupTitle+"  "+strconv.Itoa(len(choices)))
			}
			body = append(body, marker+"○ "+sideChatChoiceLabel(choice, index, len(choices)))
			if prompt := m.sidebarChooserPrompt(choice); prompt != "" {
				body = append(body, "    "+prompt)
			}
		}
	}
	footer := "Enter open · Esc back · ↑/↓ move"
	if height > 0 {
		budget := height - len(lines) - 1
		if budget < 0 {
			budget = 0
		}
		if len(body) > budget {
			body = body[:budget]
		}
	}
	lines = append(lines, body...)
	lines = append(lines, footer)
	if height > 0 && len(lines) > height {
		lines = lines[:height]
	}
	for index, line := range lines {
		lines[index] = codextui.TruncateWithEllipsis(line, width)
	}
	return strings.Join(lines, "\n")
}

// requestSideChatDiscovery starts a chooser refresh (Rust #60319
// refresh_saved_chats). Cached choices stay visible while it runs.
func (m *Model) requestSideChatDiscovery(retry bool) bubbletea.Cmd {
	if m == nil {
		return nil
	}
	parentThreadID := strings.TrimSpace(m.sidebarChooserParent)
	if parentThreadID == "" || m.onListSideChats == nil {
		m.sidebarDiscovery = SideChatDiscoveryIdle
		return nil
	}
	m.sidebarDiscoveryRequest++
	request := m.sidebarDiscoveryRequest
	m.sidebarDiscovery = SideChatDiscoveryLoading
	m.sidebarDiscoveryRetry = retry
	m.sidebarDiscoveryError = ""
	list := m.onListSideChats
	return func() bubbletea.Msg {
		choices, err := list(parentThreadID)
		return SideChatsLoadedMsg{ParentThreadID: parentThreadID, RequestID: request, Choices: choices, Err: err}
	}
}

func (m *Model) applySideChatsLoaded(msg SideChatsLoadedMsg) bubbletea.Cmd {
	if m == nil || !m.sidebarChooserOpen || strings.TrimSpace(msg.ParentThreadID) != m.sidebarChooserParent {
		return nil
	}
	if msg.RequestID != 0 && msg.RequestID != m.sidebarDiscoveryRequest {
		return nil
	}
	selected := m.sidebarChooserSelectedThreadID()
	keepNew := m.sidebarChoice == 0 && len(m.sidebarChooserChoices()) > 0
	if msg.Err != nil {
		m.sidebarDiscovery = SideChatDiscoveryFailed
		m.sidebarDiscoveryError = "Failed to load saved side chats: " + strings.TrimSpace(msg.Err.Error())
		if len(m.sidebarChooserChoices()) == 0 {
			m.sidebarChoice = m.sidebarChooserRetryRow()
		}
		m.clampSidebarChoice()
		m.refreshTranscript()
		return nil
	}
	m.sidebarChoices = mergeSideChatChoices(nil, msg.Choices)
	m.sidebarDiscovery = SideChatDiscoveryIdle
	if index := m.sidebarChooserChoiceIndex(selected); selected != "" && index > 0 {
		m.sidebarChoice = index
	} else if !keepNew {
		m.sidebarChoice = 0
	}
	m.clampSidebarChoice()
	m.refreshTranscript()
	return m.requestVisibleSideChatPrompts()
}

func (m *Model) applySideChatPrompts(msg SideChatPromptsLoadedMsg) bubbletea.Cmd {
	if m == nil || !m.sidebarChooserOpen || strings.TrimSpace(msg.ParentThreadID) != m.sidebarChooserParent {
		return nil
	}
	if msg.RequestID != 0 && msg.RequestID != m.sidebarPromptsRequest {
		return nil
	}
	if m.sidebarPrompts == nil {
		m.sidebarPrompts = map[string]string{}
	}
	for id, prompt := range msg.Prompts {
		m.sidebarPrompts[id] = prompt
	}
	m.refreshTranscript()
	return nil
}

// requestVisibleSideChatPrompts fetches previews only for the chats the
// chooser currently paints (Rust #60319 refresh_visible_side_chat_prompts).
func (m *Model) requestVisibleSideChatPrompts() bubbletea.Cmd {
	if m == nil || m.onReadSideChatPrompts == nil {
		return nil
	}
	ids := make([]string, 0, 8)
	for _, index := range m.sidebarChooserWindow(m.sidebarChooserPanelHeight()) {
		if choice, ok := m.sidebarChooserChat(index); ok {
			ids = append(ids, choice.ThreadID)
		}
	}
	if len(ids) == 0 {
		m.sidebarPrompts = map[string]string{}
		return nil
	}
	m.sidebarPromptsRequest++
	request := m.sidebarPromptsRequest
	parentThreadID := m.sidebarChooserParent
	read := m.onReadSideChatPrompts
	return func() bubbletea.Msg {
		return SideChatPromptsLoadedMsg{ParentThreadID: parentThreadID, RequestID: request, Prompts: read(ids)}
	}
}

func (m *Model) applySideOpenResult(msg SideOpenResultMsg) bubbletea.Cmd {
	if m == nil || m.State == nil {
		return nil
	}
	sideThreadID := strings.TrimSpace(msg.SideThreadID)
	parentThreadID := strings.TrimSpace(msg.ParentThreadID)
	if msg.Err != nil {
		m.notice = "Failed to open side conversation: " + strings.TrimSpace(msg.Err.Error())
		m.refreshTranscript()
		return nil
	}
	if sideThreadID == "" || parentThreadID == "" {
		return nil
	}
	if side := m.liveSideTab(sideThreadID); side != nil {
		return batchSideCommands(m.closeSidebarChooser(), m.activateRetainedSideConversation(side, ""))
	}
	side := &activeSideConversation{
		ParentThreadID:    parentThreadID,
		SideThreadID:      sideThreadID,
		ParentMessages:    cloneSideMessages(m.State.Messages),
		ParentStatus:      strings.TrimSpace(m.State.Status),
		ParentPlaceholder: m.composer.Placeholder,
		SideMessages:      cloneSideMessages(msg.Response.Messages),
		SideStatus:        firstNonEmpty(strings.TrimSpace(msg.Response.Status), "idle"),
		SidePlaceholder:   "Ask Codex in side conversation",
		ShowingSide:       true,
	}
	side.ParentVoice.syncFromModel(m)
	m.registerSidebarTab(side)
	return batchSideCommands(m.closeSidebarChooser(), m.activateRetainedSideConversation(side, ""))
}

// sidebarChooserChoices is the chooser order: discovered saved chats first,
// then retained live tabs that discovery has not reported yet, so cached chats
// stay available while a refresh runs.
func (m *Model) sidebarChooserChoices() []SideChatChoice {
	if m == nil {
		return nil
	}
	choices := mergeSideChatChoices(nil, m.sidebarChoices)
	seen := make(map[string]bool, len(choices))
	for _, choice := range choices {
		seen[choice.ThreadID] = true
	}
	for _, tab := range m.sidebarTabsFor(m.sidebarChooserParent) {
		if tab == nil {
			continue
		}
		threadID := strings.TrimSpace(tab.SideThreadID)
		if threadID == "" || seen[threadID] {
			continue
		}
		seen[threadID] = true
		choices = append(choices, SideChatChoice{ThreadID: threadID})
	}
	return choices
}

func mergeSideChatChoices(base []SideChatChoice, extra []SideChatChoice) []SideChatChoice {
	out := make([]SideChatChoice, 0, len(base)+len(extra))
	seen := map[string]bool{}
	for _, choice := range append(append([]SideChatChoice{}, base...), extra...) {
		choice.ThreadID = strings.TrimSpace(choice.ThreadID)
		if choice.ThreadID == "" || seen[choice.ThreadID] {
			continue
		}
		seen[choice.ThreadID] = true
		out = append(out, choice)
	}
	return out
}

func (m *Model) sidebarChooserRetryVisible(choices []SideChatChoice) bool {
	if m == nil {
		return false
	}
	return m.sidebarDiscovery == SideChatDiscoveryFailed ||
		(m.sidebarDiscovery == SideChatDiscoveryLoading && m.sidebarDiscoveryRetry)
}

func (m *Model) sidebarChooserRetryRow() int {
	return len(m.sidebarChooserChoices()) + 1
}

func (m *Model) sidebarChooserIsRetryRow(index int, choices []SideChatChoice) bool {
	return m.sidebarChooserRetryVisible(choices) && index == len(choices)+1
}

func (m *Model) sidebarChooserChoiceCount() int {
	choices := m.sidebarChooserChoices()
	count := len(choices) + 1
	if m.sidebarChooserRetryVisible(choices) {
		count++
	}
	return count
}

func (m *Model) sidebarChooserChat(index int) (SideChatChoice, bool) {
	choices := m.sidebarChooserChoices()
	if index < 1 || index > len(choices) {
		return SideChatChoice{}, false
	}
	return choices[index-1], true
}

func (m *Model) sidebarChooserChoiceIndex(threadID string) int {
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return 0
	}
	for index, choice := range m.sidebarChooserChoices() {
		if choice.ThreadID == threadID {
			return index + 1
		}
	}
	return 0
}

func (m *Model) sidebarChooserSelectedThreadID() string {
	if choice, ok := m.sidebarChooserChat(m.sidebarChoice); ok {
		return choice.ThreadID
	}
	return ""
}

func (m *Model) clampSidebarChoice() {
	if m == nil {
		return
	}
	count := m.sidebarChooserChoiceCount()
	if count <= 0 {
		m.sidebarChoice = 0
		return
	}
	if m.sidebarChoice < 0 {
		m.sidebarChoice = 0
	}
	if m.sidebarChoice >= count {
		m.sidebarChoice = count - 1
	}
}

func (m *Model) sidebarChooserPrompt(choice SideChatChoice) string {
	if m == nil || m.sidebarPrompts == nil {
		return ""
	}
	return firstLineText(m.sidebarPrompts[choice.ThreadID])
}

func (m *Model) sidebarChatCreationAvailable() bool {
	if m == nil || m.State == nil || strings.TrimSpace(m.State.ThreadID) == "" {
		return false
	}
	for _, message := range m.State.Messages {
		if message.Role == codextui.RoleUser {
			return true
		}
	}
	// A parent that already has side chats necessarily had a main-chat turn.
	return len(m.sidebarTabsFor(m.sidebarChooserParent)) > 0
}

func (m *Model) liveSideTab(threadID string) *activeSideConversation {
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return nil
	}
	for _, tab := range m.sidebarTabsFor(m.sidebarChooserParent) {
		if tab != nil && strings.TrimSpace(tab.SideThreadID) == threadID {
			return tab
		}
	}
	return nil
}

// handleSidebarChooserMouseRow maps a pointer row onto the painted chooser
// rows (Rust #60319 choice_at, which counts prompt preview rows). The first
// return value reports whether the chooser owns the press, the second whether
// a row was activated.
func (m *Model) handleSidebarChooserMouseRow(screenY int) (bool, bubbletea.Cmd, bool) {
	if m == nil || !m.sidebarChooserVisible() || !m.sidebarChooserFocused {
		return false, nil, false
	}
	top := m.sidebarTabStripTop
	if top < 0 {
		top = 0
	}
	row := screenY - top
	if row < 0 {
		return true, nil, false
	}
	index, ok := m.sidebarChooserChoiceAtRow(row)
	if !ok {
		return true, nil, false
	}
	m.sidebarChoice = index
	return true, m.acceptSidebarChooser(), true
}

// sidebarChooserChoiceAtRow resolves one painted chooser row to a choice index,
// reusing the same header, group header, and preview-row geometry as
// renderSidebarChooser.
func (m *Model) sidebarChooserChoiceAtRow(row int) (int, bool) {
	if m == nil || row < 4 {
		return 0, false
	}
	choices := m.sidebarChooserChoices()
	groupHeader := len(choices) > 1
	line := 4
	for _, index := range m.sidebarChooserWindow(m.sidebarChooserPanelHeight()) {
		if groupHeader && index == 1 {
			if line == row {
				return 0, false
			}
			line++
		}
		height := m.sidebarChooserRowHeight(index, choices)
		if row >= line && row < line+height {
			return index, true
		}
		line += height
	}
	return 0, false
}

func (m *Model) sidebarChooserRowHeight(index int, choices []SideChatChoice) int {
	if index == 0 {
		return 1
	}
	if m.sidebarChooserIsRetryRow(index, choices) {
		return 1
	}
	if choice, ok := m.sidebarChooserChat(index); ok && m.sidebarChooserPrompt(choice) != "" {
		return 2
	}
	return 1
}

// sidebarChooserWindow returns the choice indexes the panel paints, keeping the
// selection visible and accounting for prompt preview rows (Rust #60319
// choice_rows / visible_chats).
func (m *Model) sidebarChooserWindow(height int) []int {
	if m == nil {
		return nil
	}
	choices := m.sidebarChooserChoices()
	count := len(choices) + 1
	if m.sidebarChooserRetryVisible(choices) {
		count++
	}
	if count <= 0 {
		return nil
	}
	available := height - 5
	if height <= 0 {
		available = count * 2
	}
	if available < 1 {
		available = 1
	}
	if len(choices) > 1 && available > 1 {
		available--
	}
	choice := m.sidebarChoice
	if choice < 0 {
		choice = 0
	}
	if choice >= count {
		choice = count - 1
	}
	first := 0
	for first < choice {
		used := 0
		for index := first; index <= choice; index++ {
			used += m.sidebarChooserRowHeight(index, choices)
		}
		if used <= available {
			break
		}
		first++
	}
	window := make([]int, 0, count)
	used := 0
	for index := first; index < count; index++ {
		rowHeight := m.sidebarChooserRowHeight(index, choices)
		if used+rowHeight > available && len(window) > 0 {
			break
		}
		used += rowHeight
		window = append(window, index)
	}
	return window
}

func sideChatChoiceLabel(choice SideChatChoice, index int, total int) string {
	if name := firstLineText(choice.Name); name != "" {
		return name
	}
	if preview := firstLineText(choice.Preview); preview != "" {
		return preview
	}
	if total == 1 {
		return "Side chat"
	}
	return "Side chat " + strconv.Itoa(index)
}

func firstLineText(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if index := strings.IndexByte(value, '\n'); index >= 0 {
		value = value[:index]
	}
	return strings.TrimSpace(value)
}
