package tea

import (
	"strconv"
	"strings"
	"time"

	bubbletea "charm.land/bubbletea/v2"

	codextui "codex_go/tui"
	"codex_go/tui/keymsg"
	"codex_go/tui/markdown"
)

// sidebarLoadingGlyph mirrors Rust motion::loading_glyph for the chooser's
// discovery row: the shared frame table animates, reduced motion keeps ◌.
func (m *Model) sidebarLoadingGlyph() string {
	if m == nil {
		return codextui.LoadingGlyphReducedMotion
	}
	started := m.sidebarDiscoveryStarted
	if started.IsZero() {
		started = time.Now()
	}
	motion := codextui.MotionReduced
	if m.animationsEnabled {
		motion = codextui.MotionAnimated
	}
	return codextui.LoadingGlyph(started, motion, time.Now())
}

// Rust #59739/#59767/#59784: each parent owns one sidebar. The chooser lists
// its sibling side chats plus a "New side chat" entry, and the tab strip stays
// on the sidebar's first row while a chat is showing.

const (
	sidebarSingleTabLabel = " Side chat "
	sidebarTabWidth       = len(sidebarSingleTabLabel)
	// Rust #60319: adjacent tabs paint one noninteractive column between them.
	sidebarTabGap   = 1
	sidebarAddLabel = " + "
	// Rust sidebar_chooser::close_cell paints a one-column close glyph in the
	// last column of every tab cell, including the single " Side chat " tab.
	sidebarTabCloseGlyph = "×"
	// sidebarSingleTabCell is the painted lone tab: the title fills every column
	// but the last and the close glyph occupies the last one.
	sidebarSingleTabCell = " Side chat" + sidebarTabCloseGlyph
	// Rust sidebar_tab_strip.rs reserves both gutter columns when the tabs
	// overflow, so the tabs keep their place while the user switches between
	// them; the arrows step to the neighbouring tab.
	sidebarTabOverflowLeft   = "‹"
	sidebarTabOverflowRight  = "›"
	sidebarTabOverflowGutter = 2
	sidebarChooserTitle      = "Open in sidebar"
	sidebarNewChatTitle      = "+ New side chat"

	// Rust #60319 sidebar_chooser.rs: the new-chat row explains where a side
	// chat starts, the chooser waits for the first main-chat turn, and a failed
	// discovery offers an inline retry row.
	sidebarNewChatSubtitle          = "Available after your first message"
	sidebarNewChatSubtitleAvailable = "Start from this conversation"
	sidebarWaitingSideChats         = "You can start a side chat after your first message in the main chat."
	sidebarRetryingChatsTitle       = "Retrying…"
	sidebarRetryChatsTitle          = "Retry loading side chats"
	sidebarSideChatsGroupTitle      = "Side chats"
	// Rust sidebar_chooser.rs WIDE_WIDTH: the new-chat subtitle shares the row
	// with its title only when the body is this wide.
	sidebarChooserWideWidth = 58
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

// sideOpenState mirrors Rust's sidebar_chooser::OpenState for the saved chat
// the chooser is preparing. Rust keeps the target while it waits, while the
// attach runs, and after a failure so the row can offer a retry.
type sideOpenState string

const (
	sideOpenNone    sideOpenState = ""
	sideOpenWaiting sideOpenState = "waiting"
	sideOpenLoading sideOpenState = "loading"
	sideOpenFailed  sideOpenState = "failed"
)

// sideOpenConcurrency is Rust's saved_openings.active limit: two attaches may
// run at once and every further target waits for a slot.
const sideOpenConcurrency = 2

// sideOpenFlight is one saved-chat attach that has not reported yet (Rust
// saved_openings::Flight). A retired flight keeps its slot until it finishes so
// the client never exceeds the concurrency limit.
type sideOpenFlight struct {
	request int
	retired bool
}

// sideOpenTarget identifies one saved chat under its parent thread.
type sideOpenTarget struct {
	parentThreadID string
	sideThreadID   string
}

func sideOpenKey(parentThreadID, sideThreadID string) string {
	return strings.TrimSpace(parentThreadID) + "\x00" + strings.TrimSpace(sideThreadID)
}

// SideChatListFunc discovers the saved side chats attached to a parent.
type SideChatListFunc func(parentThreadID string) ([]SideChatChoice, error)

// SideChatPromptFunc reads the latest user prompt for the visible chats.
type SideChatPromptFunc func(threadIDs []string) map[string]string

// SidebarFilesSnapshot is one chooser inventory snapshot (Rust
// sidebar_files::Files plus the pane's preview constraint).
type SidebarFilesSnapshot struct {
	Files              []markdown.SidebarFileLink
	Loading            bool
	CWD                string
	PreviewUnavailable string
}

// SidebarFilesFunc snapshots the files the owning conversation mentioned.
type SidebarFilesFunc func(parentThreadID string) SidebarFilesSnapshot

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

// SidebarFilesLoadedMsg replaces the chooser's Files group. Hosts may also
// send it unprompted after a completed patch or generated image.
type SidebarFilesLoadedMsg struct {
	ParentThreadID string
	RequestID      uint64
	Snapshot       SidebarFilesSnapshot
}

type SideOpenResultMsg struct {
	// RequestID identifies the newest accepted open; older completions are
	// ignored after the user selects another chat or cancels (Rust #60599).
	RequestID      int
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
	m.sidebarChooserScroll = sidebarChooserScroll{}
	m.sidebarShowingFilePath = false
	m.sidebarFilePathScroll = 0
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
	// Rust paints the chooser in the side pane without a notice in the main
	// conversation, so opening it leaves the parent pane untouched.
	m.notice = ""
	m.refreshTranscript()
	discovery := m.requestSideChatDiscovery(false)
	if discovery == nil {
		discovery = m.refreshStatusControlsCmd()
	}
	return batchSideCommands(cmd, discovery, m.requestSidebarFiles())
}

func (m *Model) closeSidebarChooser() bubbletea.Cmd {
	if m == nil || !m.sidebarChooserOpen {
		return nil
	}
	// Closing the chooser owns the sidebar again, so an in-flight attach must
	// not switch conversations later (Rust #60599).
	m.cancelSideChatOpen()
	m.sidebarChooserOpen = false
	m.sidebarChooserFocused = false
	m.sidebarChooserParent = ""
	m.sidebarChoice = 0
	m.sidebarChooserScroll = sidebarChooserScroll{}
	m.sidebarShowingFilePath = false
	m.sidebarFilePathScroll = 0
	m.sidebarTabHover = -1
	m.sidebarAddHover = false
	m.notice = ""
	m.refreshTranscript()
	return m.refreshStatusControlsCmd()
}

// sidebarChooserListActions are the shared `list` context actions the chooser
// honours, in tui/keymap.go order (Rust keymap.rs list bindings).

// cancelSideChatOpen retires the attach the chooser is waiting on so its
// completion cannot switch conversations after the user cancels (Rust #60599
// sidebar_loading supersession). Other targets' flights keep running; they
// release their own slot when they report.
func (m *Model) cancelSideChatOpen() {
	if m == nil || (m.sideOpeningThreadID == "" && m.sideOpenQueued == nil) {
		return
	}
	if m.sideOpeningThreadID != "" {
		if flight, ok := m.sideOpenFlights[sideOpenKey(m.sidebarChooserParent, m.sideOpeningThreadID)]; ok {
			flight.retired = true
		}
	}
	m.sideOpenQueued = nil
	m.clearSideOpen()
}

// startSideOpen records the flight and returns the attach command (Rust
// saved_openings.active.insert + Incoming::begin).
func (m *Model) startSideOpen(parentThreadID, sideThreadID string, open SideOpenFunc) bubbletea.Cmd {
	if m == nil || open == nil {
		return nil
	}
	m.sideOpenRequest++
	request := m.sideOpenRequest
	if m.sideOpenFlights == nil {
		m.sideOpenFlights = make(map[string]*sideOpenFlight, sideOpenConcurrency)
	}
	m.sideOpenFlights[sideOpenKey(parentThreadID, sideThreadID)] = &sideOpenFlight{request: request}
	m.sideOpenQueued = nil
	m.sideOpeningThreadID = sideThreadID
	m.sideOpenState = sideOpenLoading
	m.sideOpenError = ""
	m.sideOpenStarted = time.Now()
	m.refreshTranscript()
	return func() bubbletea.Msg {
		response, err := open(parentThreadID, sideThreadID)
		return SideOpenResultMsg{RequestID: request, ParentThreadID: parentThreadID, SideThreadID: sideThreadID, Response: response, Err: err}
	}
}

// startQueuedSideOpen launches the target parked in OpenState::Waiting once a
// flight released a slot (Rust SavedChatReleased -> StartQueuedSavedChat).
func (m *Model) startQueuedSideOpen() bubbletea.Cmd {
	if m == nil || m.sideOpenQueued == nil {
		return nil
	}
	target := m.sideOpenQueued
	m.sideOpenQueued = nil
	if !m.sidebarChooserVisible() || strings.TrimSpace(m.sideOpeningThreadID) != target.sideThreadID {
		return nil
	}
	if _, inFlight := m.sideOpenFlights[sideOpenKey(target.parentThreadID, target.sideThreadID)]; inFlight ||
		len(m.sideOpenFlights) >= sideOpenConcurrency {
		m.sideOpenQueued = target
		return nil
	}
	return m.startSideOpen(target.parentThreadID, target.sideThreadID, m.onOpenSideChat)
}

// clearSideOpen drops the pending attach without touching the request counter,
// matching Rust's pane.opening = None before a chooser action.
func (m *Model) clearSideOpen() {
	if m == nil {
		return
	}
	m.sideOpeningThreadID = ""
	m.sideOpenState = sideOpenNone
	m.sideOpenError = ""
}

var sidebarChooserListActions = []string{
	"move_up", "move_down", "move_left", "move_right", "page_up", "page_down", "jump_top", "jump_bottom", "accept", "cancel",
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
		// Rust side_focus: `p` toggles the full-path view while a file row is
		// selected (or while the view is already showing).
		if msg.String() == "p" && (m.sidebarShowingFilePath ||
			(m.sidebarChooserSelectionVisible() && m.sidebarChooserSelectedFile())) {
			m.sidebarShowingFilePath = !m.sidebarShowingFilePath
			m.sidebarFilePathScroll = 0
			m.refreshTranscript()
			return true, nil
		}
		return false, nil
	}
	if m.sidebarShowingFilePath {
		// Rust: every list action except Cancel scrolls the wrapped path and is
		// consumed; Cancel closes the path view instead of the chooser.
		if action == "cancel" {
			m.sidebarShowingFilePath = false
			m.sidebarFilePathScroll = 0
			m.refreshTranscript()
			return true, nil
		}
		if file, ok := m.sidebarChooserFile(m.sidebarChoice); ok {
			m.sidebarFilePathScroll = scrollSidebarFilePath(
				m.sidebarTabStripWidth(),
				m.sidebarChooserPanelHeight(),
				file.Path,
				m.sidebarFilePathScroll,
				action,
			)
			m.refreshTranscript()
		}
		return true, nil
	}
	if !m.sidebarChatCreationAvailable() && action != "cancel" && len(m.sidebarFiles) == 0 {
		// Rust #60319: before the first main-chat turn the chooser explains the
		// wait and consumes every list action except Cancel. Files already in
		// the inventory stay reachable.
		return true, nil
	}
	switch action {
	case "move_up", "move_down", "page_up", "page_down", "jump_top", "jump_bottom":
		m.navigateSidebarChooser(action)
		return true, nil
	case "move_left", "move_right":
		// Rust app/side_focus.rs: a horizontal list move with a chat selected is
		// a Back. Without one it falls through to apply_vertical_action, which
		// ignores horizontal moves, so the key is consumed with no effect.
		if m.sidebarPaneSelectedChatID() == "" {
			return true, nil
		}
		// Back cancels a pending opening first and leaves the chooser open
		// (footer: "cancel opening"), exactly like Esc.
		if m.sideOpeningThreadID != "" && m.sideOpenState != sideOpenFailed {
			m.cancelSideChatOpen()
			m.refreshTranscript()
			return true, nil
		}
		return true, m.backToSelectedSideChat(keySpecFromKeyMsg(msg))
	case "accept":
		// Rust #60319 guard_chooser_key: a held activation key must not reach
		// the view the chooser just opened.
		m.armSelectExitGuard(keySpecFromKeyMsg(msg))
		return true, m.acceptSidebarChooser()
	case "cancel":
		// Rust app/side_focus.rs: Cancel during a pending attach cancels the
		// opening and keeps the chooser open, so the hint reads "cancel
		// opening". A failed opening (or none) falls through to Back.
		if m.sideOpeningThreadID != "" && m.sideOpenState != sideOpenFailed {
			m.cancelSideChatOpen()
			m.refreshTranscript()
			return true, nil
		}
		m.cancelSideChatOpen()
		return true, m.closeSidebarChooser()
	}
	return false, nil
}

// moveSidebarChooserChoice moves the chooser selection for the sidebar_up and
// sidebar_down leader chords (Rust #60152 leader_defaults).
func (m *Model) moveSidebarChooserChoice(delta int) {
	if m == nil || !m.sidebarChooserVisible() || !m.sidebarChooserFocused {
		return
	}
	action := "move_up"
	if delta > 0 {
		action = "move_down"
	}
	for step := 0; step < max(delta, -delta); step++ {
		m.navigateSidebarChooser(action)
	}
}

// sidebarPaneSelectedChatID is the chat the pane was last showing (Rust
// sidebar::selected_chat = visits.last()). It is the chat a horizontal list
// move returns to, independent of the row the chooser highlights.
func (m *Model) sidebarPaneSelectedChatID() string {
	if m == nil || m.activeSide == nil {
		return ""
	}
	side := m.activeSide
	if parent := strings.TrimSpace(m.sidebarChooserParent); parent != "" &&
		strings.TrimSpace(side.ParentThreadID) != parent {
		return ""
	}
	return strings.TrimSpace(side.SideThreadID)
}

// backToSelectedSideChat leaves the chooser and re-selects the chat the pane was
// showing (Rust ChooserAction::Back -> select_agent_thread(selected_chat())).
func (m *Model) backToSelectedSideChat(activation string) bubbletea.Cmd {
	if m == nil {
		return nil
	}
	side := m.activeSide
	selected := m.sidebarPaneSelectedChatID()
	closeCmd := m.closeSidebarChooser()
	cmd := closeCmd
	if side != nil && selected != "" &&
		(!side.ShowingSide || strings.TrimSpace(m.State.ThreadID) != selected) {
		cmd = batchSideCommands(closeCmd, m.activateRetainedSideConversation(side, ""))
	}
	// Rust guards the key that just left the chooser so it cannot repeat in the
	// composer it returns to.
	m.armSelectExitGuard(activation)
	return cmd
}

// sidebarTabStripVisible reports whether a tab strip is on screen: the chooser
// paints one as its first row, and a paired side chat keeps one above the
// transcript whether or not it currently owns the composer (Rust
// #59784/#59928 paint the Inactive state too).
func (m *Model) sidebarTabStripVisible() bool {
	if m == nil || !m.sideWorkspaceEnabled() {
		return false
	}
	if m.sidebarChooserVisible() {
		return true
	}
	return m.activeSide != nil
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
	// gutter is the reserved arrow columns before the tab cells when the strip
	// overflows; leftArrow/rightArrow report which arrows are painted.
	gutter     int
	leftArrow  bool
	rightArrow bool
	addStart   int
	addWidth   int
}

// sidebarTabStripWindowFor builds the window for a strip of the given width,
// keeping the selected tab in view (Rust #59784).
func sidebarTabStripWindowFor(tabs []*activeSideConversation, selected string, width int, cloud bool) sidebarTabStripWindow {
	labels := sidebarTabLabels(len(tabs), cloud)
	tabWidth, count, gutter := sidebarTabGeometry(width, len(labels))
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
	end := min(first+count, len(tabs))
	return sidebarTabStripWindow{
		tabs:       tabs,
		labels:     labels,
		tabWidth:   tabWidth,
		stride:     tabWidth + sidebarTabGap,
		count:      count,
		first:      first,
		gutter:     gutter,
		leftArrow:  gutter > 0 && first > 0,
		rightArrow: gutter > 0 && end < len(tabs),
		addStart:   available,
		addWidth:   addWidth,
	}
}

// tabCellAt maps a strip column onto the tab cell painted there and the column
// inside that cell. Gutter columns, the gaps between cells, the add button and
// the blank columns after the last tab are not tab cells.
func (w sidebarTabStripWindow) tabCellAt(column int) (index int, within int, ok bool) {
	if w.count == 0 || w.tabWidth <= 0 || w.stride <= 0 {
		return 0, 0, false
	}
	relative := column - w.gutter
	if relative < 0 {
		return 0, 0, false
	}
	offset := relative / w.stride
	if offset >= w.count {
		return 0, 0, false
	}
	within = relative - offset*w.stride
	if within >= w.tabWidth {
		// The gap between tabs is not an interactive target.
		return 0, 0, false
	}
	index = w.first + offset
	if index < 0 || index >= len(w.tabs) {
		return 0, 0, false
	}
	return index, within, true
}

// closeAt maps a strip column onto the close glyph painted in the last column
// of a tab cell (Rust sidebar_chooser::close_cell). The close cell belongs to
// the tab's click target in Rust, so it keeps the same hover highlight.
func (w sidebarTabStripWindow) closeAt(column int) int {
	if w.tabWidth < 3 {
		return -1
	}
	index, within, ok := w.tabCellAt(column)
	if !ok || within != w.tabWidth-1 {
		return -1
	}
	return index
}

// tabAt maps a strip column back to the tab painted there, or -1 for the "+"
// button and for the blank cells after the last tab.
func (w sidebarTabStripWindow) tabAt(column int) int {
	index, _, ok := w.tabCellAt(column)
	if !ok {
		return -1
	}
	return index
}

// overflowAt maps a gutter column onto the neighbouring tab the arrow steps to
// (Rust TabTarget::Overflow).
func (w sidebarTabStripWindow) overflowAt(column int) int {
	if w.leftArrow && column == 0 && w.first > 0 {
		return w.first - 1
	}
	if w.rightArrow && column == w.addStart-1 {
		if index := w.first + w.count; index < len(w.tabs) {
			return index
		}
	}
	return -1
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
	cloud := m.threadLabelCloud(m.sidebarParentThreadID(), m.backendID)
	return sidebarTabStripWindowFor(m.sidebarTabsFor(m.sidebarParentThreadID()), selected, width, cloud)
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
	return sidebarTabStripWindowFor(tabs, "", width, false).tabAt(column)
}

func sidebarTabLabels(tabCount int, cloud bool) []string {
	labels := make([]string, 0, tabCount)
	prefix := codextui.ThreadLabelPrefix(cloud)
	for index := 0; index < tabCount; index++ {
		if tabCount == 1 {
			labels = append(labels, prefix+sidebarSingleTabLabel)
			continue
		}
		labels = append(labels, prefix+" Side "+strconv.Itoa(index+1)+" ")
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

// sidebarTabGeometry mirrors Rust TabStrip::strip_targets: the cell keeps the
// fixed " Side chat " width unless the strip is narrower, the separating gap is
// part of the budget, and an overflowing strip reserves both arrow gutters so
// the cells do not shift while the user steps through them.
func sidebarTabGeometry(width int, tabCount int) (tabWidth int, count int, gutter int) {
	available, _ := sidebarTabStripSpace(width)
	if available <= 0 || tabCount <= 0 {
		return 0, 0, 0
	}
	stride := sidebarTabWidth + sidebarTabGap
	overflow := tabCount > max((available+sidebarTabGap)/stride, 1)
	if overflow && available >= 7 {
		gutter = sidebarTabOverflowGutter
	}
	space := available - 2*gutter
	tabWidth = min(sidebarTabWidth, space)
	if tabWidth <= 0 {
		return 0, 0, gutter
	}
	count = (space + sidebarTabGap) / (tabWidth + sidebarTabGap)
	if count > tabCount {
		count = tabCount
	}
	return tabWidth, count, gutter
}

// sidebarTabLayout returns the painted cell width and how many tabs fit.
func sidebarTabLayout(labels []string, width int) (int, int) {
	tabWidth, count, _ := sidebarTabGeometry(width, len(labels))
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
		// Rust side_focus.rs clears the pending opening before acting on the
		// new-chat row.
		m.clearSideOpen()
		cmd := m.closeSidebarChooser()
		m.sidebarChoice = 0
		return batchSideCommands(cmd, m.startSideConversation("/side", ""))
	case m.sidebarChooserIsRetryRow(m.sidebarChoice, choices):
		return m.requestSideChatDiscovery(true)
	default:
		if _, file := m.sidebarChooserFile(m.sidebarChoice); file {
			// Rust opens the file in a document tab (sidebar_document.rs). The
			// Go pane has no document tabs yet, so the row stays selectable but
			// inert until that port lands (#61889/#61890).
			return nil
		}
		choice, ok := m.sidebarChooserChat(m.sidebarChoice)
		if !ok {
			return nil
		}
		if side := m.liveSideTab(choice.ThreadID); side != nil {
			m.clearSideOpen()
			return batchSideCommands(m.closeSidebarChooser(), m.activateRetainedSideConversation(side, ""))
		}
		if m.onOpenSideChat == nil {
			return nil
		}
		parentThreadID := m.sidebarChooserParent
		sideThreadID := choice.ThreadID
		open := m.onOpenSideChat
		// Rust #60599: prepare saved chats in the background, keep the chooser
		// usable, and let a newer selection supersede this one. Accepting a row
		// whose attach failed retries it (OpenState::Failed -> Loading).
		flight, inFlight := m.sideOpenFlights[sideOpenKey(parentThreadID, sideThreadID)]
		if inFlight && !flight.retired {
			// The same chat is already attaching; Rust paints Loading without
			// starting a second request.
			m.sideOpeningThreadID = sideThreadID
			m.sideOpenState = sideOpenLoading
			m.sideOpenError = ""
			m.sideOpenQueued = nil
			m.refreshTranscript()
			return nil
		}
		if inFlight || len(m.sideOpenFlights) >= sideOpenConcurrency {
			// Rust saved_openings.open_saved_chat parks the target in
			// OpenState::Waiting until a flight releases its slot.
			m.sideOpeningThreadID = sideThreadID
			m.sideOpenState = sideOpenWaiting
			m.sideOpenError = ""
			m.sideOpenStarted = time.Now()
			m.sideOpenQueued = &sideOpenTarget{parentThreadID: parentThreadID, sideThreadID: sideThreadID}
			m.refreshTranscript()
			return nil
		}
		return m.startSideOpen(parentThreadID, sideThreadID, open)
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
	level := codextui.DetectStdoutColorLevel()
	chooserState := m.sidebarTabChooserState()
	chatState := m.sidebarTabChatState()
	if chooserState != sidebarTabStateAbsent {
		// Rust paints the chooser's own strip with TabState::Absent for the
		// chats, because the list below carries the selection instead.
		chatState = sidebarTabStateAbsent
	}
	styles := sidebarTabStripStylesFor(chooserState, chatState, level)
	window := m.sidebarTabStripWindowWidth(width)
	var builder strings.Builder
	column := 0
	writeSpaces := func(count int) {
		if count <= 0 {
			return
		}
		builder.WriteString(strings.Repeat(" ", count))
		column += count
	}
	if window.gutter > 0 {
		// Rust paints ‹ in the left gutter while an earlier tab is hidden and
		// keeps the reserved columns blank otherwise.
		if window.leftArrow {
			builder.WriteString(paintSidebarCell(sidebarTabOverflowLeft, styles.rest, level))
			column++
		} else {
			writeSpaces(1)
		}
		writeSpaces(window.gutter - 1)
	}
	for offset := 0; offset < window.count; offset++ {
		index := window.first + offset
		if index >= len(window.labels) {
			break
		}
		if offset > 0 {
			// Rust #60319 TAB_GAP: one blank, noninteractive column.
			writeSpaces(sidebarTabGap)
		}
		cell := renderSidebarTabCell(window.labels[index], window.tabWidth)
		spec := styles.rest
		switch {
		case chatState != sidebarTabStateAbsent && m.sidebarTabSelected(window.tabs[index]):
			spec = styles.selected
		case index == m.sidebarTabHover:
			spec = styles.hover
		}
		builder.WriteString(paintSidebarCell(cell, spec, level))
		column += window.tabWidth
	}
	if window.rightArrow {
		// The trailing arrow occupies the last column before the add button.
		writeSpaces(window.addStart - 1 - column)
		builder.WriteString(paintSidebarCell(sidebarTabOverflowRight, styles.rest, level))
		column++
	}
	writeSpaces(window.addStart - column)
	addSpec := styles.add
	if m.sidebarAddHover {
		addSpec = styles.addHover
	}
	addCell := padSidebarCell(sidebarAddLabel, window.addWidth)
	builder.WriteString(paintSidebarCell(addCell, addSpec, level))
	return codextui.TruncateWithEllipsis(builder.String(), width)
}

// sidebarTabState mirrors Rust sidebar_tab_strip.rs TabState, which decides
// how much surface a strip cell carries.
type sidebarTabState int

const (
	sidebarTabStateAbsent sidebarTabState = iota
	sidebarTabStateActive
	sidebarTabStateInactive
)

// sidebarTabStripStyles is the resolved style set the strip paints with: the
// quiet chip every cell starts from, the shaded hover chip, the selected
// chat's chip, and the "+" button's resting and hovered chips.
type sidebarTabStripStyles struct {
	rest     codextui.StyleSpec
	hover    codextui.StyleSpec
	selected codextui.StyleSpec
	add      codextui.StyleSpec
	addHover codextui.StyleSpec
}

// sidebarTabStripStylesFor resolves Rust sidebar_tab_strip::render's styles:
// `rest` is secondary text on the user-message surface, `hover` shades that
// surface twice toward the terminal background, and the selected chat keeps the
// shared selection fill.
func sidebarTabStripStylesFor(chooser, chat sidebarTabState, level codextui.StdoutColorLevel) sidebarTabStripStyles {
	hover := codextui.HoverStyleFor(codextui.StyleSpec{}, level)
	styles := sidebarTabStripStyles{
		rest:     codextui.SecondarySurfaceStyle(level),
		hover:    hover,
		selected: codextui.SecondarySurfaceStyle(level),
		add:      codextui.SecondaryTextStyle(level),
		addHover: hover,
	}
	switch chat {
	case sidebarTabStateActive:
		styles.selected = selectionNotUnderlined(level)
	case sidebarTabStateInactive:
		// Rust paints a paired-but-not-displayed chat with the hover chip in
		// bold so the strip still shows where the other chat lives.
		styles.selected = hover
		styles.selected.Bold = true
	}
	switch chooser {
	case sidebarTabStateActive:
		styles.add = selectionNotUnderlined(level)
		styles.addHover = codextui.HoverStyleFor(styles.add, level)
	case sidebarTabStateInactive:
		styles.add = codextui.QuietWorkspaceStyle(level)
	}
	return styles
}

// selectionNotUnderlined resolves Rust's `selection_style().not_underlined()`.
func selectionNotUnderlined(level codextui.StdoutColorLevel) codextui.StyleSpec {
	background, known := codextui.DefaultBackgroundRGB()
	spec := codextui.SelectionStyleFor(nil, level)
	if known {
		spec = codextui.SelectionStyleFor(&background, level)
	}
	spec.Underlined = false
	return spec
}

// sidebarTabChatState reports whether the strip is painting the displayed side
// chat (Active), another tab while a side pair exists (Inactive), or no side
// chat at all (Absent), mirroring Rust owned_transcript.rs's `chat`.
func (m *Model) sidebarTabChatState() sidebarTabState {
	if m == nil || m.activeSide == nil {
		return sidebarTabStateAbsent
	}
	if m.activeSide.ShowingSide {
		return sidebarTabStateActive
	}
	return sidebarTabStateInactive
}

// sidebarTabChooserState reports the "+" button's state: Active while the
// chooser owns focus, Inactive while the chooser is painted but the composer
// does, and Absent in the plain side-pane strip (Rust `chooser`).
func (m *Model) sidebarTabChooserState() sidebarTabState {
	if m == nil || !m.sidebarChooserVisible() {
		return sidebarTabStateAbsent
	}
	if m.sidebarChooserFocused {
		return sidebarTabStateActive
	}
	return sidebarTabStateInactive
}

// sidebarTabSelected reports whether the strip tab is the chat the pane has
// selected (Rust sidebar::selected_chat).
func (m *Model) sidebarTabSelected(tab *activeSideConversation) bool {
	if m == nil || m.activeSide == nil || tab == nil {
		return false
	}
	selected := strings.TrimSpace(m.activeSide.SideThreadID)
	return selected != "" && selected == strings.TrimSpace(tab.SideThreadID)
}

// paintSidebarCell wraps one strip cell in its SGR prefix, like the per-cell
// styles ratatui writes into the buffer.
func paintSidebarCell(cell string, spec codextui.StyleSpec, level codextui.StdoutColorLevel) string {
	if cell == "" {
		return ""
	}
	prefix := codextui.SGRForStyleQuiet(spec, level)
	if prefix == "" {
		return cell
	}
	return prefix + cell + "\x1b[0m"
}

func padSidebarCell(label string, width int) string {
	if width <= 0 {
		return ""
	}
	if codextui.DisplayWidth(label) >= width {
		return codextui.TruncateToWidth(label, width)
	}
	return label + strings.Repeat(" ", width-codextui.DisplayWidth(label))
}

// renderSidebarTabCell paints one tab like Rust's sidebar_chooser strip: the
// title fills every column but the last, and the close glyph occupies that
// trailing column (close_cell is tab.right()-1).
func renderSidebarTabCell(label string, width int) string {
	if width <= 0 {
		return ""
	}
	if width < 3 {
		return padSidebarCell(label, width)
	}
	return padSidebarCell(label, width-1) + sidebarTabCloseGlyph
}

// renderSidebarChooser paints the chooser panel: the tab strip, discovery
// status, the new-chat row, the saved chats with their latest prompt previews,
// an inline retry row after a discovery failure, and the footer hints.
func (m *Model) renderSidebarChooser(width int, height int) string {
	if m == nil || width <= 0 {
		return ""
	}
	return joinSidebarChooserRows(m.sidebarChooserRows(width, height), width)
}

// sidebarChooserRows paints the chooser into a fixed-height row buffer the way
// Rust sidebar_chooser::paint does: the tab strip owns row 0, the title row 2,
// the discovery state row 3, the choice window starts at FIRST_CHOICE_ROW, and
// the footer hint owns the pane's last row.
func (m *Model) sidebarChooserRows(width int, height int) []string {
	if height <= 0 {
		height = m.sidebarChooserPanelHeight()
	}
	height = max(height, 1)
	rows := make([]string, height)
	footer := height - 1
	set := func(row int, text string) {
		if row < 0 || row >= footer {
			return
		}
		rows[row] = text
	}
	rows[0] = m.renderSidebarTabStrip(width)
	if height <= 1 {
		return rows
	}
	finish := func() []string {
		rows[footer] = m.sidebarChooserFooter(sidebarChooserBodyWidth(width))
		return rows
	}
	if m.sideStartPending {
		set(2, "Starting side conversation...")
		return finish()
	}
	choiceRows := m.sidebarChoiceRowsFor(width, height)
	waiting := !m.sidebarChatCreationAvailable() && len(m.sidebarFiles) == 0
	top := 0
	if !waiting {
		top = choiceRows.top(m.sidebarChooserScroll, m.sidebarChooserClampedChoice())
	}
	paintAt := func(offset int, text string) {
		y := offset - top
		if y >= sidebarChooserBodyStart && y < sidebarChooserBodyStart+choiceRows.height {
			set(y, text)
		}
	}
	paintAt(2, "\x1b[1m"+sidebarChooserTitle+"\x1b[22m")
	if waiting {
		// Rust paints the waiting note wrapped from FIRST_CHOICE_ROW and skips
		// discovery entirely until the parent has its first user turn.
		for offset, text := range wrapSidebarChooserText(sidebarWaitingSideChats, sidebarChooserBodyWidth(width)) {
			paintAt(sidebarChooserFirstChoiceRow+offset, "\x1b[2m"+text+"\x1b[22m")
		}
		return finish()
	}
	switch m.sidebarDiscovery {
	case SideChatDiscoveryLoading:
		label := "Loading side chats…"
		if len(m.sidebarChooserChoices()) > 0 {
			label = "Refreshing side chats…"
		}
		paintAt(3, "\x1b[2m"+m.sidebarLoadingGlyph()+" "+label+"\x1b[22m")
	case SideChatDiscoveryFailed:
		paintAt(3, "\x1b[31m"+m.sidebarDiscoveryError+"\x1b[39m")
	default:
		if m.sidebarFilesLoading {
			paintAt(3, "\x1b[2mLoading older files…\x1b[22m")
		}
	}
	m.paintSidebarChooserBody(width, height, choiceRows, top, set)
	// Rust keeps the scroll arrows in the right margin, outside the clickable
	// list rows.
	if choiceRows.height > 0 && width >= 3 {
		if top > 0 {
			set(sidebarChooserBodyStart, "\x1b[2m↑\x1b[22m")
		}
		if top < choiceRows.maxTop() {
			set(footer-1, "\x1b[2m↓\x1b[22m")
		}
	}
	return finish()
}

// paintSidebarChooserBody is Rust sidebar_chooser::paint after the header rows:
// the new-chat entry, the group headers, the visible chats, the retry row and
// the file inventory, all offset by the shared scroll top.
func (m *Model) paintSidebarChooserBody(width int, height int, rows sidebarChoiceRows, top int, set func(int, string)) {
	choices := m.sidebarChooserChoices()
	choice := m.sidebarChooserClampedChoice()
	bodyWidth := sidebarChooserBodyWidth(width)
	if m.sidebarShowingFilePath {
		m.paintSidebarChooserFilePath(width, height, bodyWidth, set)
		return
	}
	paintAt := func(offset int, text string) {
		y := offset - top
		if y >= sidebarChooserBodyStart && y < sidebarChooserBodyStart+rows.height {
			set(y, text)
		}
	}
	marker := func(index int) string {
		if index == choice {
			return "› "
		}
		return "  "
	}
	markerCell := func(index int) string {
		if index == choice {
			return "›"
		}
		return " "
	}
	// New side chat: Rust paints the subtitle inline when the body is wide and
	// on its own indented row otherwise.
	{
		title := marker(0) + sidebarNewChatTitle
		detail := sidebarNewChatSubtitle
		if m.sidebarChatCreationAvailable() {
			detail = sidebarNewChatSubtitleAvailable
		}
		if rows.wide {
			pad := max(bodyWidth-1-codextui.DisplayWidth(detail), codextui.DisplayWidth(title))
			title += strings.Repeat(" ", pad-codextui.DisplayWidth(title))
			paintAt(sidebarChooserFirstChoiceRow, truncateSidebarChooserRow(title+detail, bodyWidth))
		} else {
			paintAt(sidebarChooserFirstChoiceRow, truncateSidebarChooserRow(title, bodyWidth))
			paintAt(sidebarChooserFirstChoiceRow+1, truncateSidebarChooserRow("    "+detail, bodyWidth))
		}
	}
	if rows.chatsHeader >= 0 {
		paintAt(rows.chatsHeader, "\x1b[2m"+sidebarSideChatsGroupTitle+"  "+strconv.Itoa(len(choices))+"\x1b[22m")
	}
	if rows.filesHeader >= 0 {
		group := "Files"
		markdownOnly := len(m.sidebarFiles) > 0
		for _, file := range m.sidebarFiles {
			if !markdown.IsMarkdownFile(file.Path) {
				markdownOnly = false
				break
			}
		}
		if markdownOnly {
			group = "Markdown files"
		}
		paintAt(rows.filesHeader, "\x1b[2m"+group+"  "+strconv.Itoa(len(m.sidebarFiles))+"\x1b[22m")
	}
	for _, visible := range rows.visible(m.sidebarChooserScroll, choice) {
		index := visible.index
		if m.sidebarChooserIsRetryRow(index, choices) {
			label := sidebarRetryChatsTitle
			if m.sidebarDiscovery == SideChatDiscoveryLoading {
				label = sidebarRetryingChatsTitle
			}
			paintAt(visible.rows.start, truncateSidebarChooserRow(markerCell(index)+"   "+label, bodyWidth))
			continue
		}
		if file, ok := m.sidebarChooserFile(index); ok {
			m.paintSidebarFileRow(file, markerCell(index), rows.wide, bodyWidth, visible.rows, paintAt)
			continue
		}
		chat, ok := m.sidebarChooserChat(index)
		if !ok {
			continue
		}
		label := sideChatChoiceLabel(chat, index, len(choices))
		if m.threadLabelCloud(m.sidebarParentThreadID(), m.backendID) {
			label = codextui.ThreadLabelPrefix(true) + label
		}
		row := marker(index) + "○ " + label
		if status := m.sideOpenStatusLabel(chat.ThreadID); status != "" {
			// Rust reserves the status width before truncating the title so the
			// pending state stays visible on narrow panes.
			budget := max(bodyWidth-6, 1)
			titleWidth := max(budget-codextui.DisplayWidth(status), budget/3)
			row = marker(index) + "○ " + truncateSidebarChooserRow(label, max(titleWidth, 1)) + "  " + status
		}
		paintAt(visible.rows.start, truncateSidebarChooserRow(row, bodyWidth))
		if visible.rows.len() == 2 {
			if prompt := m.sidebarChooserPrompt(chat); prompt != "" {
				paintAt(visible.rows.start+1, truncateSidebarChooserRow("    "+prompt, bodyWidth))
			}
		}
	}
}

// paintSidebarFileRow is Rust paint's file arm: the relative path split into
// directory and file name, summarised from both ends when the pane is narrow.
func (m *Model) paintSidebarFileRow(file markdown.SidebarFileLink, marker string, wide bool, bodyWidth int, lines sidebarChooserSpan, paintAt func(int, string)) {
	path := sidebarChooserRelativePath(m.sidebarFilesCwd, file.Path)
	directory, name := splitSidebarFilePath(path)
	if m.sidebarFilePreviewUnavailable != "" {
		directory = m.sidebarFilePreviewUnavailable
	}
	width := max(bodyWidth-4, 0)
	if wide {
		name = codextui.FilenameLabel(name, max(width-5, 0))
		directoryWidth := max(width-codextui.DisplayWidth(name)-2, 0)
		directory = codextui.MiddleLabel(directory, directoryWidth)
		paintAt(lines.start, truncateSidebarChooserRow(marker+" · "+name+"  "+directory, bodyWidth))
		return
	}
	name = codextui.FilenameLabel(name, width)
	paintAt(lines.start, truncateSidebarChooserRow(marker+" · "+name, bodyWidth))
	if lines.start+1 < lines.end {
		directory = codextui.MiddleLabel(directory, width)
		paintAt(lines.start+1, truncateSidebarChooserRow("    "+directory, bodyWidth))
	}
}

// paintSidebarChooserFilePath is Rust paint's full_path view: the caption, a
// blank row and the wrapped path window above the footer.
func (m *Model) paintSidebarChooserFilePath(width int, height int, bodyWidth int, set func(int, string)) {
	file, ok := m.sidebarChooserFile(m.sidebarChooserClampedChoice())
	if !ok {
		return
	}
	pathRows, visible := sidebarChooserFilePathLayout(width, height, file.Path)
	top := 0
	if limit := max(len(pathRows)-visible, 0); m.sidebarFilePathScroll > limit {
		top = limit
	} else {
		top = max(m.sidebarFilePathScroll, 0)
	}
	row := 2
	if height > 2 {
		set(row, "\x1b[1mFile path\x1b[22m")
		set(row+1, "")
		row += 2
	}
	for index := top; index < len(pathRows) && index < top+visible; index++ {
		set(row, truncateSidebarChooserRow(pathRows[index], bodyWidth))
		row++
	}
}

// sidebarChooserRelativePath shows a path relative to the pane's workspace when
// it lives underneath it, and otherwise keeps the original destination.
func sidebarChooserRelativePath(cwd string, path string) string {
	cwd = strings.TrimRight(strings.TrimSpace(cwd), "/\\")
	if cwd == "" || path == "" {
		return path
	}
	if len(path) <= len(cwd) {
		return path
	}
	if !strings.EqualFold(path[:len(cwd)], cwd) {
		return path
	}
	rest := path[len(cwd):]
	if rest[0] != '/' && rest[0] != '\\' {
		return path
	}
	return strings.TrimLeft(rest, "/\\")
}

// splitSidebarFilePath is Rust `path.rsplit_once(['/', '\\'])`: a bare file name
// lives at the project root.
func splitSidebarFilePath(path string) (string, string) {
	if index := strings.LastIndexAny(path, "/\\"); index >= 0 {
		return path[:index], path[index+1:]
	}
	return "Project root", path
}

func truncateSidebarChooserRow(text string, width int) string {
	return codextui.TruncateWithEllipsis(text, width)
}

// wrapSidebarChooserText wraps on word boundaries the way textwrap::wrap does
// for the chooser's waiting note.
func wrapSidebarChooserText(text string, width int) []string {
	if text == "" {
		return nil
	}
	if width <= 0 {
		return []string{text}
	}
	lines := make([]string, 0, 2)
	current := ""
	for _, word := range strings.Fields(text) {
		switch {
		case current == "":
			current = word
		case codextui.DisplayWidth(current)+1+codextui.DisplayWidth(word) <= width:
			current += " " + word
		default:
			lines = append(lines, current)
			current = word
		}
	}
	if current != "" {
		lines = append(lines, current)
	}
	return lines
}

// sideOpenStatusLabel paints the pending saved-chat state appended to its row
// (Rust sidebar_chooser.rs OpenState arms).
func (m *Model) sideOpenStatusLabel(threadID string) string {
	if m == nil || m.sideOpeningThreadID == "" {
		return ""
	}
	if strings.TrimSpace(m.sideOpeningThreadID) != strings.TrimSpace(threadID) {
		return ""
	}
	switch m.sideOpenState {
	case sideOpenWaiting:
		return "\x1b[2mWaiting to open…\x1b[22m"
	case sideOpenFailed:
		return "\x1b[31mRetry · " + m.sideOpenError + "\x1b[39m"
	default:
		return "\x1b[2mOpening…\x1b[22m"
	}
}

// sidebarChooserFooter paints the footer hint line from the shared keymap
// (Rust sidebar_chooser::footer): accept flips to retry, cancel flips to
// cancel opening, movement only exists once a side chat can start, and the
// global hide-pane chord always closes the pane.
func (m *Model) sidebarChooserFooter(width int) string {
	if m == nil {
		return ""
	}
	if m.sidebarShowingFilePath {
		hints := make([]string, 0, 2)
		if key := m.sidebarChooserListHint("cancel"); key != "" {
			hints = append(hints, key+" back")
		}
		if key := m.sidebarChooserListHint("move_down"); key != "" {
			hints = append(hints, key+" scroll")
		}
		return fitSidebarChooserHints(hints, width)
	}
	available := m.sidebarChatCreationAvailable()
	fileSelected := m.sidebarChooserSelectedFile()
	visible := m.sidebarChooserSelectionVisible()
	hints := make([]string, 0, 4)
	if visible && (available || fileSelected) && (!fileSelected || m.sidebarFilePreviewUnavailable == "") {
		if key := m.sidebarChooserListHint("accept"); key != "" {
			hints = append(hints, key+" "+m.sidebarChooserAcceptLabel())
		}
	}
	if key := m.sidebarChooserListHint("cancel"); key != "" {
		hints = append(hints, key+" "+m.sidebarChooserCancelLabel())
	}
	if fileSelected && visible && m.sidebarChooserFilePathKeyAvailable() {
		hints = append(hints, "p path")
	}
	if available || len(m.sidebarFiles) > 0 {
		up := m.sidebarChooserListHint("move_up")
		down := m.sidebarChooserListHint("move_down")
		if up != "" && down != "" {
			hints = append(hints, up+"/"+down+" move")
		}
	}
	if key := m.sidebarChooserHideHint(); key != "" {
		hints = append(hints, key+" hide pane")
	}
	return fitSidebarChooserHints(hints, width)
}

// sidebarChooserFilePathKeyAvailable is Rust file_path_key_available: `p` only
// opens the path view when no list binding claims the key.
func (m *Model) sidebarChooserFilePathKeyAvailable() bool {
	if m == nil {
		return false
	}
	for _, action := range sidebarChooserListActions {
		bindings, _, _ := codextui.ResolvedKeymapBindings(m.keymapConfig, "list", action)
		for _, binding := range bindings {
			if strings.TrimSpace(binding) == "p" {
				return false
			}
		}
	}
	return true
}

// fitSidebarChooserHints drops whole hints that do not fit and marks the cut
// with an ellipsis, the way Rust footer_hint::HintLine::new measures them.
func fitSidebarChooserHints(hints []string, width int) string {
	var builder strings.Builder
	end := 0
	for index, hint := range hints {
		separator := 0
		if index > 0 {
			separator = 3
		}
		start := end + separator
		next := start + codextui.DisplayWidth(hint)
		overflowSpace := 0
		if index < len(hints)-1 {
			overflowSpace = 2
		}
		if next+overflowSpace > width {
			if width > 0 {
				if index == 0 {
					builder.WriteString("…")
				} else {
					builder.WriteString(" …")
				}
			}
			return builder.String()
		}
		if index > 0 {
			builder.WriteString(" · ")
		}
		builder.WriteString(hint)
		end = next
	}
	return builder.String()
}

func (m *Model) sidebarChooserListHint(action string) string {
	bindings, _, _ := codextui.ResolvedKeymapBindings(m.keymapConfig, "list", action)
	if len(bindings) == 0 {
		return ""
	}
	return codextui.KeybindingDisplayLabel(strings.TrimSpace(bindings[0]))
}

func (m *Model) sidebarChooserAcceptLabel() string {
	if m.sidebarChooserRetrySelected() {
		return "retry"
	}
	return "open"
}

// sidebarChooserRetrySelected mirrors Rust footer: accept retries on the
// discovery retry row or on a chat whose attach failed.
func (m *Model) sidebarChooserRetrySelected() bool {
	choices := m.sidebarChooserChoices()
	if m.sidebarChooserIsRetryRow(m.sidebarChoice, choices) {
		return true
	}
	choice, ok := m.sidebarChooserChat(m.sidebarChoice)
	if !ok || m.sideOpenState != sideOpenFailed {
		return false
	}
	return strings.TrimSpace(m.sideOpeningThreadID) == strings.TrimSpace(choice.ThreadID)
}

func (m *Model) sidebarChooserCancelLabel() string {
	if m.sideOpeningThreadID != "" && m.sideOpenState != sideOpenFailed {
		return "cancel opening"
	}
	return "back"
}

// sidebarChooserHideHint resolves the global hide-pane leader chord the way
// Rust footer's primary_hint(Global, "hide_pane") does.
func (m *Model) sidebarChooserHideHint() string {
	prefixes, err := codextui.LeaderKeybindings(m.keymapConfig)
	if err != nil || len(prefixes) == 0 {
		return ""
	}
	prefix := prefixes[0]
	bindings, _, custom := codextui.ResolvedKeymapBindings(m.keymapConfig, "global", "hide_pane")
	if custom && len(bindings) > 0 {
		for _, binding := range bindings {
			if completion, ok := codextui.LeaderSpecCompletion(binding); ok {
				return codextui.KeybindingDisplayLabel(prefix + " " + completion)
			}
		}
		return codextui.KeybindingDisplayLabel(strings.TrimSpace(bindings[0]))
	}
	if m.leaderPrefixClaimed(prefix, "global", "hide_pane") {
		return ""
	}
	return codextui.KeybindingDisplayLabel(prefix + " -")
}

// joinSidebarChooserRows clips the tab strip to the pane and every body row to
// Rust's body_row geometry: one leading inset column and two reserved trailing
// columns (sidebar_chooser.rs body_row).
func joinSidebarChooserRows(rows []string, width int) string {
	bodyWidth := max(width-3, 1)
	out := make([]string, len(rows))
	for index, row := range rows {
		if index == 0 {
			out[index] = codextui.TruncateWithEllipsis(row, width)
			continue
		}
		out[index] = " " + codextui.TruncateWithEllipsis(row, bodyWidth)
	}
	return strings.Join(out, "\n")
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
	m.sidebarDiscoveryStarted = time.Now()
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
	selectedFile := m.sidebarChooserSelectedFilePath()
	keepNew := m.sidebarChoice == 0 && len(m.sidebarChooserChoices()) > 0
	if msg.Err != nil {
		m.sidebarDiscovery = SideChatDiscoveryFailed
		// Rust paints Discovery::Failed(error) verbatim in red; the error text
		// already names the failing request.
		m.sidebarDiscoveryError = strings.TrimSpace(msg.Err.Error())
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
	m.restoreSidebarFileSelection(selectedFile)
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

// requestSidebarFiles refreshes the chooser's Files group (Rust
// refresh_sidebar_files on opening the chooser).
func (m *Model) requestSidebarFiles() bubbletea.Cmd {
	if m == nil {
		return nil
	}
	parentThreadID := strings.TrimSpace(m.sidebarChooserParent)
	if parentThreadID == "" || m.onSidebarFiles == nil {
		m.sidebarFilesLoading = false
		return nil
	}
	m.sidebarFilesRequest++
	request := m.sidebarFilesRequest
	m.sidebarFilesLoading = true
	load := m.onSidebarFiles
	return func() bubbletea.Msg {
		return SidebarFilesLoadedMsg{ParentThreadID: parentThreadID, RequestID: request, Snapshot: load(parentThreadID)}
	}
}

// applySidebarFilesLoaded replaces the Files group and keeps a selected file
// selected when the inventory is reordered or extended (Rust
// restore_file_selection).
func (m *Model) applySidebarFilesLoaded(msg SidebarFilesLoadedMsg) bubbletea.Cmd {
	if m == nil || !m.sidebarChooserOpen || strings.TrimSpace(msg.ParentThreadID) != m.sidebarChooserParent {
		return nil
	}
	if msg.RequestID != 0 && msg.RequestID != m.sidebarFilesRequest {
		return nil
	}
	selected := m.sidebarChooserSelectedFilePath()
	m.sidebarFiles = mergeSidebarFileLinks(nil, msg.Snapshot.Files)
	m.sidebarFilesLoading = msg.Snapshot.Loading
	m.sidebarFilesCwd = strings.TrimSpace(msg.Snapshot.CWD)
	m.sidebarFilePreviewUnavailable = strings.TrimSpace(msg.Snapshot.PreviewUnavailable)
	m.restoreSidebarFileSelection(selected)
	m.clampSidebarChoice()
	if m.sidebarShowingFilePath && !m.sidebarChooserSelectedFile() {
		m.sidebarShowingFilePath = false
		m.sidebarFilePathScroll = 0
	}
	m.refreshTranscript()
	return nil
}

// sidebarChooserSelectedFilePath is the path the Files group currently
// highlights, or empty when a chat row is selected.
func (m *Model) sidebarChooserSelectedFilePath() string {
	if m == nil {
		return ""
	}
	file, ok := m.sidebarChooserFile(m.sidebarChooserClampedChoice())
	if !ok {
		return ""
	}
	return file.Path
}

// restoreSidebarFileSelection points the selection back at a file after the
// inventory changed (Rust SidebarPane::restore_file_selection).
func (m *Model) restoreSidebarFileSelection(path string) {
	if m == nil || path == "" {
		return
	}
	for index, file := range m.sidebarFiles {
		if file.Path == path {
			m.sidebarChoice = m.sidebarChooserFileStart() + index
			return
		}
	}
}

// mergeSidebarFileLinks keeps the inventory order and drops duplicate
// destinations, filling in a location a later mention provided.
func mergeSidebarFileLinks(base []markdown.SidebarFileLink, extra []markdown.SidebarFileLink) []markdown.SidebarFileLink {
	out := make([]markdown.SidebarFileLink, 0, len(extra))
	byPath := make(map[string]int, len(extra))
	for _, file := range append(append([]markdown.SidebarFileLink{}, base...), extra...) {
		if file.Path == "" {
			continue
		}
		if index, ok := byPath[file.Path]; ok {
			if out[index].Location == nil {
				out[index].Location = file.Location
			}
			continue
		}
		byPath[file.Path] = len(out)
		out = append(out, file)
	}
	return out
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
	key := sideOpenKey(parentThreadID, sideThreadID)
	flight, inFlight := m.sideOpenFlights[key]
	if !inFlight || (msg.RequestID != 0 && msg.RequestID != flight.request) {
		// The flight was retired (cancelled or replaced), so its completion
		// must not touch the pane or free another flight's slot.
		return nil
	}
	delete(m.sideOpenFlights, key)
	// Rust installs an attach only while the pane still wants that target; a
	// superseded or cancelled flight is released instead.
	wanted := !flight.retired && strings.TrimSpace(m.sideOpeningThreadID) == sideThreadID
	if msg.Err != nil {
		// Rust sidebar_loading keeps the target with OpenState::Failed so the
		// chooser row paints "Retry · {error}" and Enter retries it.
		if wanted && m.sidebarChooserVisible() {
			m.sideOpenState = sideOpenFailed
			m.sideOpenError = strings.TrimSpace(msg.Err.Error())
			m.notice = ""
		} else if wanted {
			m.clearSideOpen()
			m.notice = "Failed to open side conversation: " + strings.TrimSpace(msg.Err.Error())
		}
		m.refreshTranscript()
		return m.startQueuedSideOpen()
	}
	if !wanted {
		m.refreshTranscript()
		return m.startQueuedSideOpen()
	}
	m.clearSideOpen()
	if sideThreadID == "" || parentThreadID == "" {
		return m.startQueuedSideOpen()
	}
	if side := m.liveSideTab(sideThreadID); side != nil {
		return batchSideCommands(m.closeSidebarChooser(), m.activateRetainedSideConversation(side, ""))
	}
	// Rust #60599: live notifications that arrived while the saved chat was
	// loading are buffered; replay them only when the resumed snapshot is
	// empty, mirroring the agent-switch rule that avoids duplicate items.
	sideMessages := cloneSideMessages(msg.Response.Messages)
	if buffered := m.backgroundThreadEvents[sideThreadID]; len(buffered) > 0 {
		if len(sideMessages) == 0 {
			sideMessages = applyBufferedThreadReplay(sideMessages, filterResolvedMisalignmentReplayEvents(buffered))
		}
		delete(m.backgroundThreadEvents, sideThreadID)
	}
	side := &activeSideConversation{
		ParentThreadID:    parentThreadID,
		SideThreadID:      sideThreadID,
		ParentMessages:    cloneSideMessages(m.State.Messages),
		ParentStatus:      strings.TrimSpace(m.State.Status),
		ParentPlaceholder: m.composer.Placeholder,
		SideMessages:      sideMessages,
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
	return m.sidebarChooserChoiceCountFor(m.sidebarChooserChoices())
}

// sidebarChooserFileStartFor is Rust ChatTabs::file_start: the new-chat row,
// every saved chat, the optional retry row, and then the files.
func (m *Model) sidebarChooserFileStartFor(choices []SideChatChoice) int {
	start := len(choices) + 1
	if m.sidebarChooserRetryVisible(choices) {
		start++
	}
	return start
}

func (m *Model) sidebarChooserFileStart() int {
	return m.sidebarChooserFileStartFor(m.sidebarChooserChoices())
}

// sidebarChooserFileOffset maps a chooser index onto the Files group (Rust
// ChatTabs::file_offset).
func (m *Model) sidebarChooserFileOffset(index int) (int, bool) {
	if m == nil {
		return 0, false
	}
	offset := index - m.sidebarChooserFileStart()
	if offset < 0 || offset >= len(m.sidebarFiles) {
		return 0, false
	}
	return offset, true
}

func (m *Model) sidebarChooserFile(index int) (markdown.SidebarFileLink, bool) {
	offset, ok := m.sidebarChooserFileOffset(index)
	if !ok {
		return markdown.SidebarFileLink{}, false
	}
	return m.sidebarFiles[offset], true
}

func (m *Model) sidebarChooserChoiceCountFor(choices []SideChatChoice) int {
	return m.sidebarChooserFileStartFor(choices) + len(m.sidebarFiles)
}

// sidebarChooserClampedChoice is the selection Rust's choice_rows measures:
// the stored index clamped into the current choice count.
func (m *Model) sidebarChooserClampedChoice() int {
	if m == nil {
		return 0
	}
	count := m.sidebarChooserChoiceCount()
	switch {
	case count <= 0 || m.sidebarChoice < 0:
		return 0
	case m.sidebarChoice >= count:
		return count - 1
	}
	return m.sidebarChoice
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
	return m.sidebarChatCreationAvailableFor(m.sidebarChooserParent)
}

// sidebarChatCreationAvailableFor reports whether the parent has the first
// user turn that Rust requires before a side chat can be created.
func (m *Model) sidebarChatCreationAvailableFor(parentThreadID string) bool {
	if m == nil || m.State == nil || strings.TrimSpace(m.State.ThreadID) == "" {
		return false
	}
	for _, message := range m.State.Messages {
		if message.Role == codextui.RoleUser {
			return true
		}
	}
	// A parent that already has side chats necessarily had a main-chat turn.
	return len(m.sidebarTabsFor(parentThreadID)) > 0
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

// Rust sidebar_chooser.rs geometry: the new-chat entry starts at
// FIRST_CHOICE_ROW and the footer hint owns the pane's last row.
const sidebarChooserFirstChoiceRow = 4

// sidebarChooserBodyWidth is Rust body_row: one leading inset column and two
// reserved trailing columns.
func sidebarChooserBodyWidth(width int) int {
	return max(width-3, 1)
}

// sidebarChooserChoiceAtRow resolves one painted chooser row to a choice index
// using the same layout the pane painted (Rust sidebar_chooser::choice_at).
func (m *Model) sidebarChooserChoiceAtRow(row int) (int, bool) {
	if m == nil {
		return 0, false
	}
	rows := m.sidebarChoiceRowsFor(m.sidebarTabStripWidth(), m.sidebarChooserPanelHeight())
	choice := m.sidebarChooserClampedChoice()
	top := rows.top(m.sidebarChooserScroll, choice)
	start := sidebarChooserBodyStart + top
	end := start + rows.height
	for _, visible := range rows.visible(m.sidebarChooserScroll, choice) {
		first := max(visible.rows.start, start)
		last := min(visible.rows.end, end)
		span := sidebarChooserSpan{start: first, end: last}
		if first >= last || !span.contains(row) {
			continue
		}
		if _, file := m.sidebarChooserFile(visible.index); file && m.sidebarFilePreviewUnavailable != "" {
			return 0, false
		}
		if visible.index == 0 && !m.sidebarChatCreationAvailable() {
			return 0, false
		}
		return visible.index, true
	}
	return 0, false
}

// sidebarChooserSpan is a half-open range of painted chooser rows.
type sidebarChooserSpan struct{ start, end int }

func (s sidebarChooserSpan) len() int {
	if s.end <= s.start {
		return 0
	}
	return s.end - s.start
}

func (s sidebarChooserSpan) contains(row int) bool {
	return row >= s.start && row < s.end
}

// sidebarChooserVisibleChat is one chat row the pane paints: its choice index
// and the rows it occupies (a title row plus an optional prompt preview).
type sidebarChooserVisibleChat struct {
	index int
	rows  sidebarChooserSpan
}

// sidebarChooserWindow returns the chat indexes the panel paints for a pane
// height, sharing the layout with the renderer.
func (m *Model) sidebarChooserWindow(height int) []int {
	if m == nil {
		return nil
	}
	rows := m.sidebarChoiceRowsFor(m.sidebarTabStripWidth(), height)
	choice := m.sidebarChooserClampedChoice()
	window := make([]int, 0, 8)
	for _, visible := range rows.visible(m.sidebarChooserScroll, choice) {
		if _, chat := m.sidebarChooserChat(visible.index); chat {
			window = append(window, visible.index)
		}
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
