package tea

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	bubbletea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	codextui "codex_go/tui"
	"codex_go/tui/keymsg"
	"codex_go/tui/markdown"
	mousemsg "codex_go/tui/mousemsg"
)

// Rust parity: codex-rs/tui/src/app/sidebar_document.rs (#61888/#61889/#61890).
//
// The chooser's file rows open a read-only preview in its own sidebar tab.
// Markdown renders as Markdown; source opens at the requested line with a
// line-number gutter. A read that fails keeps the reason on the pane instead of
// retrying on its own, and a connection change invalidates an in-flight read.

// SidebarDocumentRequest asks the owning conversation for one file preview.
// Link is the clicked link's index inside the document, or -1 when the row was
// opened directly.
type SidebarDocumentRequest struct {
	ParentThreadID string
	SourceThreadID string
	Path           string
	Link           int
	Location       *markdown.SidebarSourceLocation
}

// SidebarDocumentTarget is the resolved source position of a link target.
type SidebarDocumentTarget struct {
	Line   int
	Column int
	Offset int
}

// SidebarDocumentView is one loaded preview. Err is the read failure the pane
// shows; an empty Err with Code false and Text set renders Markdown.
type SidebarDocumentView struct {
	Path       string
	Err        string
	Text       string
	Code       bool
	Language   string
	LineStarts []int
	Target     *SidebarDocumentTarget
}

// SidebarDocumentFunc loads one document preview. The call blocks; the model
// runs it as a command so the pane paints its loading state first.
type SidebarDocumentFunc func(request SidebarDocumentRequest) SidebarDocumentView

// SidebarDocumentLoadedMsg delivers a finished read to the tab that asked.
type SidebarDocumentLoadedMsg struct {
	ParentThreadID string
	ID             string
	View           SidebarDocumentView
}

// sidebarDocumentTab is one open preview tab.
type sidebarDocumentTab struct {
	ID      string
	Parent  string
	Source  string
	Path    string
	View    SidebarDocumentView
	Loading bool
	// Started anchors the loading glyph (Rust State::Loading(Instant)).
	Started time.Time
	Scroll  int
	// Link is the selected document link index, or -1 (Rust Document::link).
	Link int
	// HoverLink is the link under the pointer, or -1 (Rust
	// TranscriptView::paint_document_links emphasizes the hovered destination).
	HoverLink int
	// Find is this preview's reader search (Rust TranscriptView::search).
	Find sidebarDocumentFind
	// TargetCleared records that Esc removed the coordinate highlight (Rust
	// Document::target.take()).
	TargetCleared bool

	renderWidth int
	renderLines []string
	rendered    bool
	linkRowsFor int
	linkRows    []sidebarDocumentLinkRow
	linkRowsSet bool
}

// sidebarDocumentsFor returns the document tabs opened under one parent, in
// the order the strip paints them.
func (m *Model) sidebarDocumentsFor(parentThreadID string) []*sidebarDocumentTab {
	if m == nil || len(m.sidebarDocuments) == 0 {
		return nil
	}
	return m.sidebarDocuments[strings.TrimSpace(parentThreadID)]
}

// sidebarDocumentActiveTab is the preview the pane shows, or nil when the
// chooser (or a side chat) owns the sidebar.
func (m *Model) sidebarDocumentActiveTab() *sidebarDocumentTab {
	if m == nil || strings.TrimSpace(m.sidebarDocumentActive) == "" {
		return nil
	}
	parent := m.sidebarParentThreadID()
	for _, tab := range m.sidebarDocumentsFor(parent) {
		if tab != nil && tab.ID == m.sidebarDocumentActive {
			return tab
		}
	}
	return nil
}

// sidebarDocumentVisible reports whether a preview owns the sidebar pane.
func (m *Model) sidebarDocumentVisible() bool {
	if m == nil || !m.sideWorkspaceEnabled() {
		return false
	}
	return m.sidebarDocumentActiveTab() != nil
}

// openSidebarFileDocument opens the row's file in a preview tab (Rust
// `open_resolved_document`): an existing tab for the same path is focused
// again instead of duplicated, and a failed tab is replaced.
func (m *Model) openSidebarFileDocument(link markdown.SidebarFileLink) bubbletea.Cmd {
	return m.openSidebarDocumentAt(link.Path, -1, link.Location)
}

// openSidebarDocumentAt opens one path, optionally at a link's coordinate.
// Rust `open_resolved_document` focuses an already-loaded document and replaces
// a failed one; the Go pane re-reads when the coordinate changes so the app
// side stays the single owner of source_target maths.
func (m *Model) openSidebarDocumentAt(path string, linkIndex int, location *markdown.SidebarSourceLocation) bubbletea.Cmd {
	if m == nil || m.onSidebarDocument == nil {
		return nil
	}
	parent := strings.TrimSpace(m.sidebarChooserParent)
	if parent == "" {
		parent = m.sidebarParentThreadID()
	}
	path = strings.TrimSpace(path)
	if parent == "" || path == "" {
		return nil
	}
	// Rust reuses a loaded document for the same backend and path, and drops a
	// failed one so the reopen can retry.
	for _, tab := range m.sidebarDocumentsFor(parent) {
		if tab == nil || strings.TrimSpace(tab.Path) != path {
			continue
		}
		if tab.Loading {
			// The same file is already opening; focus it instead of starting a
			// second read (Rust focuses the existing document).
			m.sidebarDocumentActive = tab.ID
			m.sidebarChooserFocused = true
			m.sidebarTabHover = -1
			m.refreshTranscript()
			return nil
		}
		if tab.View.Err != "" {
			// A failed preview is replaced so reopening retries the read.
			m.closeSidebarDocumentTab(tab.ID)
			break
		}
		if location != nil || tab.View.Code {
			return m.reloadSidebarDocument(tab, linkIndex, location)
		}
		m.sidebarDocumentActive = tab.ID
		m.sidebarChooserFocused = true
		m.sidebarTabHover = -1
		m.refreshTranscript()
		return nil
	}
	m.sidebarDocumentCounter++
	m.recordSidebarOpenedFile(parent, path)
	id := "document-" + strconv.FormatUint(m.sidebarDocumentCounter, 10)
	tab := &sidebarDocumentTab{
		ID:        id,
		Parent:    parent,
		Source:    parent,
		Link:      -1,
		HoverLink: -1,
		Path:      path,
		Loading:   true,
		Started:   time.Now(),
	}
	m.sidebarDocuments[parent] = append(m.sidebarDocuments[parent], tab)
	m.sidebarDocumentActive = id
	m.sidebarChooserFocused = true
	m.sidebarTabHover = -1
	m.sidebarAddHover = false
	m.refreshTranscript()
	open := m.onSidebarDocument
	request := SidebarDocumentRequest{
		ParentThreadID: parent,
		SourceThreadID: parent,
		Path:           path,
		Link:           linkIndex,
		Location:       location,
	}
	return func() bubbletea.Msg {
		return SidebarDocumentLoadedMsg{ParentThreadID: parent, ID: id, View: open(request)}
	}
}

// reloadSidebarDocument re-reads one tab in place with a new coordinate.
func (m *Model) reloadSidebarDocument(tab *sidebarDocumentTab, linkIndex int, location *markdown.SidebarSourceLocation) bubbletea.Cmd {
	if m == nil || tab == nil || m.onSidebarDocument == nil {
		return nil
	}
	tab.Loading = true
	tab.Started = time.Now()
	tab.Link = -1
	tab.HoverLink = -1
	tab.TargetCleared = false
	tab.Scroll = 0
	tab.rendered = false
	tab.renderLines = nil
	tab.linkRowsSet = false
	m.sidebarDocumentActive = tab.ID
	m.sidebarChooserFocused = true
	m.sidebarTabHover = -1
	m.refreshTranscript()
	open := m.onSidebarDocument
	request := SidebarDocumentRequest{
		ParentThreadID: tab.Parent,
		SourceThreadID: tab.Source,
		Path:           tab.Path,
		Link:           linkIndex,
		Location:       location,
	}
	return func() bubbletea.Msg {
		return SidebarDocumentLoadedMsg{ParentThreadID: tab.Parent, ID: tab.ID, View: open(request)}
	}
}

// applySidebarDocumentLoaded stores a finished read on the tab that asked for
// it. A tab closed while the read ran ignores the result.
func (m *Model) applySidebarDocumentLoaded(msg SidebarDocumentLoadedMsg) bubbletea.Cmd {
	if m == nil {
		return nil
	}
	parent := strings.TrimSpace(msg.ParentThreadID)
	for _, tab := range m.sidebarDocumentsFor(parent) {
		if tab == nil || tab.ID != msg.ID {
			continue
		}
		tab.Loading = false
		tab.View = msg.View
		if path := strings.TrimSpace(msg.View.Path); path != "" {
			tab.Path = path
		}
		tab.rendered = false
		tab.renderLines = nil
		tab.linkRowsSet = false
		tab.HoverLink = -1
		if tab.Find.active() {
			// Rust keeps the query and caret across a reload and rescans the
			// freshly loaded rows.
			m.refreshSidebarDocumentFindMatches(tab, false)
		}
		m.refreshTranscript()
		return nil
	}
	return nil
}

// closeSidebarDocumentTab removes one preview. Closing the visible tab returns
// the pane to the chooser that opened it (Rust `close_sidebar_tab`).
func (m *Model) closeSidebarDocumentTab(id string) bubbletea.Cmd {
	if m == nil || strings.TrimSpace(id) == "" {
		return nil
	}
	parent := m.sidebarParentThreadID()
	tabs := m.sidebarDocumentsFor(parent)
	kept := tabs[:0]
	for _, tab := range tabs {
		if tab != nil && tab.ID == id {
			continue
		}
		kept = append(kept, tab)
	}
	if len(kept) == 0 {
		delete(m.sidebarDocuments, parent)
	} else {
		m.sidebarDocuments[parent] = kept
	}
	if m.sidebarDocumentActive == id {
		m.sidebarDocumentActive = ""
	}
	m.sidebarTabHover = -1
	m.refreshTranscript()
	return nil
}

// sidebarDocumentLinkRow is one document link resolved onto a rendered row.
type sidebarDocumentLinkRow struct {
	Index int
	Row   int // -1 when the rendered text does not repeat the destination
	Path  string
	// Column is the destination's display column inside the rendered row, so a
	// row carrying several destinations still hit-tests the one under the
	// pointer (Rust resolves the pointer's column through the link spans).
	Column int
}

// sidebarDocumentLinks lists the local file links a rendered Markdown preview
// carries, in document order (Rust `document_links`). Source previews have no
// reader links, so they select nothing.
func (m *Model) sidebarDocumentLinks(tab *sidebarDocumentTab) []markdown.SidebarFileLink {
	if tab == nil || tab.Loading || tab.View.Err != "" || tab.View.Code {
		return nil
	}
	return markdown.SidebarFileLinks(tab.View.Text)
}

// sidebarDocumentLinkRows maps each link onto the rendered row that shows its
// destination. The renderer paints the label and then the resolved target, so
// the destination search runs forward and never reuses a row.
func (m *Model) sidebarDocumentLinkRows(tab *sidebarDocumentTab) []sidebarDocumentLinkRow {
	lines := m.sidebarDocumentLines(tab, m.sidebarDocumentBodyWidth())
	if tab.linkRowsSet && tab.linkRowsFor == tab.renderWidth {
		return tab.linkRows
	}
	links := m.sidebarDocumentLinks(tab)
	if len(links) == 0 {
		tab.linkRowsSet = true
		tab.linkRowsFor = tab.renderWidth
		tab.linkRows = nil
		return nil
	}
	rows := make([]sidebarDocumentLinkRow, 0, len(links))
	searchFrom := 0
	for index, link := range links {
		row := -1
		column := 0
		path := strings.TrimSpace(link.Path)
		for candidate := searchFrom; candidate < len(lines) && path != ""; candidate++ {
			if offset := strings.Index(ansi.Strip(lines[candidate]), path); offset >= 0 {
				row = candidate
				column = codextui.DisplayWidth(ansi.Strip(lines[candidate])[:offset])
				searchFrom = candidate
				break
			}
		}
		rows = append(rows, sidebarDocumentLinkRow{Index: index, Row: row, Path: path, Column: column})
	}
	tab.linkRowsSet = true
	tab.linkRowsFor = tab.renderWidth
	tab.linkRows = rows
	return rows
}

// sidebarDocumentLinkAtRow returns the document-order index of the link painted
// on a rendered row, or -1. Rust resolves the pointer through the reader's row
// layout; the preview keeps the same rows in sidebarDocumentLinkRows.
func (m *Model) sidebarDocumentLinkAtRow(tab *sidebarDocumentTab, row int, column int) int {
	if tab == nil || row < 0 {
		return -1
	}
	best, bestDistance := -1, 0
	for _, linkRow := range m.sidebarDocumentLinkRows(tab) {
		if linkRow.Row < 0 || linkRow.Row != row {
			continue
		}
		width := codextui.DisplayWidth(linkRow.Path)
		if column >= linkRow.Column && column < linkRow.Column+width {
			return linkRow.Index
		}
		distance := linkRow.Column - column
		if column >= linkRow.Column+width {
			distance = column - (linkRow.Column + width)
		}
		if best < 0 || distance < bestDistance {
			best, bestDistance = linkRow.Index, distance
		}
	}
	return best
}

// sidebarDocumentPointerRow maps a side-pane pointer row onto the rendered
// preview row under it. Row 0 is the tab strip and the last row is the footer,
// so only body rows resolve (Rust Document::paint reserves the same bands).
func (m *Model) sidebarDocumentPointerRow(tab *sidebarDocumentTab, y int, height int) (int, bool) {
	if tab == nil || y < 1 || y > height-2 {
		return 0, false
	}
	row := tab.Scroll + (y - 1)
	if row < 0 || row >= len(m.sidebarDocumentLines(tab, m.sidebarDocumentBodyWidth())) {
		return 0, false
	}
	return row, true
}

// setSidebarDocumentHover tracks the link under the pointer so the renderer
// can emphasize it. Moving outside the pane clears the hover.
func (m *Model) setSidebarDocumentHover(tab *sidebarDocumentTab, msg mousemsg.MouseMsg, inside bool, height int, column int) {
	if m == nil || tab == nil {
		return
	}
	hover := -1
	if inside {
		if row, ok := m.sidebarDocumentPointerRow(tab, msg.Y, height); ok {
			hover = m.sidebarDocumentLinkAtRow(tab, row, column)
		}
	}
	if tab.HoverLink == hover {
		return
	}
	tab.HoverLink = hover
	m.refreshTranscript()
}

// handleSidebarDocumentClick opens the link under a left press and clears the
// cycled link first, like Rust's pointer handler setting `doc.link = None`.
func (m *Model) handleSidebarDocumentClick(tab *sidebarDocumentTab, y int, height int, column int) bubbletea.Cmd {
	if m == nil || tab == nil {
		return nil
	}
	// Rust clears the cycled link before handling a pointer press.
	if tab.Link >= 0 {
		tab.Link = -1
		m.refreshTranscript()
	}
	row, ok := m.sidebarDocumentPointerRow(tab, y, height)
	if !ok {
		return nil
	}
	index := m.sidebarDocumentLinkAtRow(tab, row, column)
	if index < 0 {
		return nil
	}
	return m.followSidebarDocumentLinkIndex(tab, index)
}

// cycleSidebarDocumentLink moves the selection like Rust `cycle_document_link`:
// Tab starts at the first link, BackTab at the last, and the chooser scrolls
// the selected link into view.
func (m *Model) cycleSidebarDocumentLink(tab *sidebarDocumentTab, forward bool) {
	rows := m.sidebarDocumentLinkRows(tab)
	if len(rows) == 0 {
		tab.Link = -1
		m.refreshTranscript()
		return
	}
	switch {
	case tab.Link < 0 && forward:
		tab.Link = 0
	case tab.Link < 0:
		tab.Link = len(rows) - 1
	case forward:
		tab.Link = (tab.Link + 1) % len(rows)
	default:
		tab.Link = (tab.Link + len(rows) - 1) % len(rows)
	}
	if tab.Link < len(rows) {
		m.sidebarDocumentRevealRow(tab, rows[tab.Link].Row)
	}
	m.refreshTranscript()
}

// sidebarDocumentRevealRow scrolls the viewport so a row is visible.
func (m *Model) sidebarDocumentRevealRow(tab *sidebarDocumentTab, row int) {
	if m == nil || tab == nil || row < 0 {
		return
	}
	height := m.sidebarDocumentViewportHeight()
	if row < tab.Scroll {
		tab.Scroll = row
	}
	if row >= tab.Scroll+height {
		tab.Scroll = row - height + 1
	}
	tab.Scroll = clampModalInt(tab.Scroll, 0, m.sidebarDocumentMaxScroll(tab))
}

// followSidebarDocumentLink opens the selected link, resolving a relative
// destination against the preview's own directory (Rust `OpenDocument`).
func (m *Model) followSidebarDocumentLink(tab *sidebarDocumentTab) bubbletea.Cmd {
	if tab == nil {
		return nil
	}
	return m.followSidebarDocumentLinkIndex(tab, tab.Link)
}

// followSidebarDocumentLinkIndex opens the link with the given document order
// index. Rust reaches this from both Enter on a cycled link and a mouse click
// on a painted destination (ViewAction::OpenDocument).
func (m *Model) followSidebarDocumentLinkIndex(tab *sidebarDocumentTab, index int) bubbletea.Cmd {
	if tab == nil || index < 0 {
		return nil
	}
	links := m.sidebarDocumentLinks(tab)
	if index >= len(links) {
		return nil
	}
	link := links[index]
	link.Path = resolveSidebarDocumentLinkPath(tab.Path, link.Path)
	tab.Link = -1
	tab.HoverLink = -1
	return m.openSidebarDocumentAt(link.Path, -1, link.Location)
}

// resolveSidebarDocumentLinkPath resolves a document link the way Rust's
// `LegacyAppPathString::resolve_against` does: absolute targets stay as
// written and relative ones join the preview's parent directory.
func resolveSidebarDocumentLinkPath(documentPath string, linkPath string) string {
	parent := documentPath
	if index := strings.LastIndexAny(parent, `/\`); index >= 0 {
		parent = parent[:index]
	}
	return joinSidebarDocumentPath(parent, linkPath)
}

// joinSidebarDocumentPath joins a relative target onto a base directory,
// keeping absolute targets as written and matching the base's separator.
func joinSidebarDocumentPath(base string, linkPath string) string {
	linkPath = strings.TrimSpace(linkPath)
	if linkPath == "" {
		return linkPath
	}
	if isAbsoluteSidebarPath(linkPath) || strings.HasPrefix(linkPath, `\\`) {
		return linkPath
	}
	base = strings.TrimSpace(base)
	if base == "" {
		return linkPath
	}
	separator := "/"
	if strings.Contains(base, `\`) && !strings.Contains(base, "/") {
		separator = `\`
	}
	return cleanSidebarDocumentPath(base+separator+linkPath, separator)
}

// recordSidebarOpenedFile keeps the preview's path in the parent's opened list
// and in a chooser that is already painted, like Rust's `opened_files` push in
// `open_resolved_document`.
func (m *Model) recordSidebarOpenedFile(parent string, path string) {
	if m == nil {
		return
	}
	parent = strings.TrimSpace(parent)
	path = strings.TrimSpace(path)
	if parent == "" || path == "" {
		return
	}
	if !isAbsoluteSidebarPath(path) && !strings.HasPrefix(path, `\\`) {
		path = joinSidebarDocumentPath(m.sidebarFilesCwd, path)
	}
	if m.sidebarOpenedFiles == nil {
		m.sidebarOpenedFiles = map[string][]markdown.SidebarFileLink{}
	}
	for _, existing := range m.sidebarOpenedFiles[parent] {
		if existing.Path == path {
			return
		}
	}
	link := markdown.SidebarFileLink{Path: path}
	m.sidebarOpenedFiles[parent] = append(m.sidebarOpenedFiles[parent], link)
	if !m.sidebarChooserOpen || parent != m.sidebarChooserParent {
		return
	}
	selected := m.sidebarChooserSelectedFilePath()
	m.sidebarFiles = mergeSidebarFileLinks(nil, append(m.sidebarFiles, link))
	m.restoreSidebarFileSelection(selected)
	m.clampSidebarChoice()
}

// cleanSidebarDocumentPath collapses `.` and `..` segments without touching
// the root prefix.
func cleanSidebarDocumentPath(value string, separator string) string {
	parts := strings.Split(value, separator)
	stack := make([]string, 0, len(parts))
	rooted := false
	for index, part := range parts {
		switch part {
		case "":
			if index == 0 {
				rooted = true
			}
		case ".":
			continue
		case "..":
			if len(stack) > 0 && stack[len(stack)-1] != ".." {
				stack = stack[:len(stack)-1]
			} else if !rooted {
				stack = append(stack, part)
			}
		default:
			stack = append(stack, part)
		}
	}
	joined := strings.Join(stack, separator)
	if rooted {
		return separator + joined
	}
	if joined == "" {
		return "."
	}
	return joined
}

// stepSidebarDocumentTab moves the strip selection by one tab, wrapping around
// the chats and previews (Rust SidebarPane::adjacent_tab + step_sidebar_tab).
func (m *Model) stepSidebarDocumentTab(step int) {
	if m == nil || step == 0 {
		return
	}
	parent := m.sidebarParentThreadID()
	chats := m.sidebarTabsFor(parent)
	documents := m.sidebarDocumentsFor(parent)
	total := len(chats) + len(documents)
	if total == 0 {
		return
	}
	current := 0
	for index, document := range documents {
		if document != nil && document.ID == m.sidebarDocumentActive {
			current = len(chats) + index
		}
	}
	next := ((current+step)%total + total) % total
	if offset := next - len(chats); offset >= 0 {
		if offset < len(documents) && documents[offset] != nil {
			m.sidebarDocumentActive = documents[offset].ID
			m.sidebarChooserFocused = true
			m.sidebarTabHover = -1
			m.refreshTranscript()
		}
		return
	}
	m.selectSidebarTab(next)
}

// updateSidebarDocumentKey handles the preview's own keys while the sidebar
// owns input (Rust `handle_document_input`).
func (m *Model) updateSidebarDocumentKey(msg keymsg.KeyMsg) (bool, bubbletea.Cmd) {
	tab := m.sidebarDocumentActiveTab()
	if tab == nil || !m.sidebarChooserFocused {
		return false, nil
	}
	if tab.Find.active() {
		return m.updateSidebarDocumentFindKey(tab, msg)
	}
	if m.keyMatches("global", "find_transcript", keySpecFromKeyMsg(msg)) ||
		m.keyMatches("pager", "find", keySpecFromKeyMsg(msg)) {
		// Rust handle_document_input starts the reader's own Find, not the
		// transcript overlay, while the preview owns the pane.
		m.beginSidebarDocumentFind(tab)
		return true, nil
	}
	action := sidebarChooserListAction(msg)
	if action == "" {
		switch msg.Type {
		case keymsg.KeyTab:
			m.cycleSidebarDocumentLink(tab, true)
			return true, nil
		case keymsg.KeyShiftTab:
			m.cycleSidebarDocumentLink(tab, false)
			return true, nil
		}
	}
	if tab.Link >= 0 && action != "cancel" && action != "accept" {
		// Rust clears the link selection on any other pager or list key.
		tab.Link = -1
	}
	switch action {
	case "move_up":
		m.scrollSidebarDocument(tab, -1)
		return true, nil
	case "move_down":
		m.scrollSidebarDocument(tab, 1)
		return true, nil
	case "page_up":
		m.scrollSidebarDocument(tab, -m.sidebarDocumentViewportHeight())
		return true, nil
	case "page_down":
		m.scrollSidebarDocument(tab, m.sidebarDocumentViewportHeight())
		return true, nil
	case "jump_top":
		tab.Scroll = 0
		m.refreshTranscript()
		return true, nil
	case "jump_bottom":
		tab.Scroll = m.sidebarDocumentMaxScroll(tab)
		m.refreshTranscript()
		return true, nil
	case "cancel":
		// Rust Esc clears the selected link, then the coordinate highlight,
		// and only then closes the tab.
		if tab.Link >= 0 {
			tab.Link = -1
			m.refreshTranscript()
			return true, nil
		}
		if tab.View.Target != nil && !tab.TargetCleared {
			tab.TargetCleared = true
			m.refreshTranscript()
			return true, nil
		}
		m.suppressCurrentViewKeyUntilRelease()
		return true, m.closeSidebarDocumentTab(tab.ID)
	case "accept":
		if cmd := m.followSidebarDocumentLink(tab); cmd != nil {
			return true, cmd
		}
		return true, nil
	case "move_left", "move_right":
		// Rust keeps the strip shortcut on Left/Right while the reader owns
		// the pane.
		return true, nil
	}
	return false, nil
}

// sidebarDocumentViewportHeight is the row count the preview scrolls in.
func (m *Model) sidebarDocumentViewportHeight() int {
	if m == nil {
		return 0
	}
	return max(m.sidebarChooserPanelHeight()-2, 1)
}

func (m *Model) scrollSidebarDocument(tab *sidebarDocumentTab, delta int) {
	if m == nil || tab == nil {
		return
	}
	tab.Scroll = clampModalInt(tab.Scroll+delta, 0, m.sidebarDocumentMaxScroll(tab))
	m.refreshTranscript()
}

func (m *Model) sidebarDocumentMaxScroll(tab *sidebarDocumentTab) int {
	if m == nil || tab == nil {
		return 0
	}
	lines := m.sidebarDocumentLines(tab, m.sidebarDocumentBodyWidth())
	return max(len(lines)-m.sidebarDocumentViewportHeight(), 0)
}

// sidebarDocumentBodyWidth is the preview's text column count inside the pane.
func (m *Model) sidebarDocumentBodyWidth() int {
	return max(m.sidebarTabStripWidth()-2, 1)
}

// sidebarDocumentLines renders the preview body for the current pane width,
// caching the result until the width or the read changes.
func (m *Model) sidebarDocumentLines(tab *sidebarDocumentTab, width int) []string {
	if tab == nil || width <= 0 {
		return nil
	}
	if tab.rendered && tab.renderWidth == width {
		return tab.renderLines
	}
	tab.renderWidth = width
	tab.rendered = true
	tab.renderLines = m.buildSidebarDocumentLines(tab, width)
	return tab.renderLines
}

func (m *Model) buildSidebarDocumentLines(tab *sidebarDocumentTab, width int) []string {
	view := tab.View
	if tab.Loading {
		started := tab.Started
		if started.IsZero() {
			started = time.Now()
		}
		return []string{"\x1b[2m" + m.sidebarLoadingGlyphAt(started) + " Opening…\x1b[22m"}
	}
	if view.Err != "" {
		message := "Could not open file: " + view.Err
		lines := wrapSidebarChooserText(message, width)
		for index := range lines {
			lines[index] = "\x1b[31m" + lines[index] + "\x1b[39m"
		}
		return lines
	}
	if view.Code {
		return m.sidebarDocumentCodeLines(view, width)
	}
	theme := m.activeTUITheme()
	rendered, err := markdown.RenderUniformListsWithThemeCwd(view.Text, width, theme, "")
	if err != nil || strings.TrimSpace(rendered) == "" {
		rendered = view.Text
	}
	raw := strings.Split(strings.ReplaceAll(rendered, "\r\n", "\n"), "\n")
	lines := make([]string, 0, len(raw))
	for _, line := range raw {
		lines = append(lines, codextui.TruncateToWidth(line, width))
	}
	return lines
}

// sidebarDocumentCodeLines lays the source out with Rust's gutter geometry:
// the line number sits in its own width, and the gutter only exists when the
// pane leaves the text more than 40 columns (transcript_view/document.rs).
func (m *Model) sidebarDocumentCodeLines(view SidebarDocumentView, width int) []string {
	source := strings.Split(strings.ReplaceAll(view.Text, "\r\n", "\n"), "\n")
	if last := len(source) - 1; last >= 0 && source[last] == "" {
		source = source[:last]
	}
	digits := sidebarDocumentDigits(len(view.LineStarts))
	gutter := 0
	if digits > 0 && width > 40+digits {
		gutter = digits + 1
	}
	textWidth := max(width-gutter, 1)
	highlighted := source
	if view.Language != "" {
		if code := codextui.HighlightCodeANSI(view.Text, view.Language, m.activeTUITheme()); strings.TrimSpace(code) != "" {
			candidate := strings.Split(strings.ReplaceAll(code, "\r\n", "\n"), "\n")
			if len(candidate) == len(source) {
				highlighted = candidate
			}
		}
	}
	lines := make([]string, 0, len(highlighted))
	for index, line := range highlighted {
		text := codextui.TruncateToWidth(line, textWidth)
		if gutter > 0 {
			number := strconv.Itoa(index + 1)
			text = strings.Repeat(" ", max(digits-len(number), 0)) + number + " " + text
		}
		lines = append(lines, text)
	}
	return lines
}

// sidebarDocumentDigits is the gutter width for a line count: log10 + 1.
func sidebarDocumentDigits(lineCount int) int {
	if lineCount <= 0 {
		return 0
	}
	digits := 0
	for lineCount > 0 {
		digits++
		lineCount /= 10
	}
	return digits
}

// sidebarDocumentTitle is the tab label (Rust `document_title`): the shortest
// path suffix that tells the open documents apart, widened while two documents
// would still collide and numbered from the end when they do.
func (m *Model) sidebarDocumentTitle(tab *sidebarDocumentTab, width int) string {
	if tab == nil || width <= 0 {
		return ""
	}
	documents := m.sidebarDocumentsFor(tab.Parent)
	path := tab.Path
	parts := splitSidebarDocumentPath(path)
	depth := 1
	for _, other := range documents {
		if other == nil || other == tab || other.Path == path {
			continue
		}
		otherParts := splitSidebarDocumentPath(other.Path)
		shared := 0
		for shared < len(parts) && shared < len(otherParts) && parts[shared] == otherParts[shared] {
			shared++
		}
		depth = max(depth, shared+1)
	}
	depth = min(depth, len(parts))
	label := codextui.DocumentLabel(parts[:depth], width)
	collision := false
	index := 1
	for _, other := range documents {
		if other == nil || other == tab {
			continue
		}
		otherParts := splitSidebarDocumentPath(other.Path)
		if len(otherParts) > depth {
			otherParts = otherParts[:depth]
		}
		if codextui.DocumentLabel(otherParts, width) == label {
			collision = true
			if other.Path < path {
				index++
			}
		}
	}
	if !collision {
		return label
	}
	prefix := fmt.Sprintf("%d:", index)
	if len(prefix) > width {
		return label
	}
	return prefix + codextui.DocumentLabel(parts[:depth], width-len(prefix))
}

func splitSidebarDocumentPath(path string) []string {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil
	}
	parts := strings.FieldsFunc(path, func(r rune) bool { return r == '/' || r == '\\' })
	if len(parts) == 0 {
		return []string{path}
	}
	// Rust's document_title splits from the right, so the file name leads and
	// DocumentLabel can widen toward the shared directory suffix.
	reversed := make([]string, 0, len(parts))
	for index := len(parts) - 1; index >= 0; index-- {
		reversed = append(reversed, parts[index])
	}
	return reversed
}

// renderSidebarDocument paints the preview: the shared tab strip, the body,
// and the reader's footer hints (Rust `Document::paint`).
func (m *Model) renderSidebarDocument(width int, height int) string {
	if m == nil || width <= 0 {
		return ""
	}
	height = max(height, 1)
	rows := make([]string, height)
	rows[0] = m.renderSidebarTabStrip(width)
	tab := m.sidebarDocumentActiveTab()
	if tab == nil || height <= 1 {
		return strings.Join(rows, "\n")
	}
	footer := height - 1
	contentHeight := max(footer-1, 0)
	bodyWidth := max(width-2, 1)
	lines := m.sidebarDocumentLines(tab, bodyWidth)
	tab.Scroll = clampModalInt(tab.Scroll, 0, max(len(lines)-contentHeight, 0))
	targetRow := -1
	if tab.View.Target != nil && !tab.TargetCleared {
		targetRow = tab.View.Target.Line - 1
	}
	linkRows := m.sidebarDocumentLinkRows(tab)
	linkPath := ""
	if tab.Link >= 0 && tab.Link < len(linkRows) {
		linkPath = linkRows[tab.Link].Path
	}
	hoverRow, hoverPath := -1, ""
	if tab.HoverLink >= 0 && tab.HoverLink < len(linkRows) && tab.HoverLink != tab.Link {
		hoverRow = linkRows[tab.HoverLink].Row
		hoverPath = linkRows[tab.HoverLink].Path
	}
	matchRow := -1
	if match, ok := tab.Find.selected(); ok {
		matchRow = match
	}
	for index := 0; index < contentHeight && tab.Scroll+index < len(lines); index++ {
		row := tab.Scroll + index
		line := lines[row]
		if row == hoverRow {
			line = boldSidebarDocumentLink(line, hoverPath)
		}
		if linkPath != "" {
			line = highlightSidebarDocumentLink(line, linkPath)
		}
		if row == targetRow {
			line = paintSidebarDocumentTarget(line, bodyWidth)
		}
		if row == matchRow {
			line = reverseVideoTranscriptLine(line)
		}
		rows[1+index] = " " + codextui.TruncateToWidth(line, bodyWidth)
	}
	rows[footer] = " " + m.sidebarDocumentFooterLine(bodyWidth, tab)
	return strings.Join(rows, "\n")
}

// sidebarDocumentFooter mirrors Rust's reader hints: read, find, link, select,
// close. The Go pane only wires reading and closing so far.
func (m *Model) sidebarDocumentFooter(width int, tab *sidebarDocumentTab) string {
	if m == nil || tab == nil {
		return ""
	}
	hints := make([]string, 0, 4)
	up := m.sidebarChooserListHint("move_up")
	down := m.sidebarChooserListHint("move_down")
	if up != "" && down != "" {
		hints = append(hints, up+"/"+down+" read")
	}
	if len(m.sidebarDocumentLinks(tab)) > 0 {
		hints = append(hints, "tab link")
	}
	if key := m.sidebarChooserListHint("cancel"); key != "" {
		if tab.Link >= 0 || (tab.View.Target != nil && !tab.TargetCleared) {
			hints = append(hints, key+" clear highlight")
		} else {
			hints = append(hints, key+" close")
		}
	}
	return fitSidebarChooserHints(hints, width)
}

// paintSidebarDocumentTarget shades the coordinate link's row (Rust
// render_document paints user_message_style over the target line).
func paintSidebarDocumentTarget(line string, width int) string {
	level := codextui.DetectStdoutColorLevel()
	spec := selectionNotUnderlined(level)
	prefix := codextui.SGRForStyleQuiet(spec, level)
	if prefix == "" {
		return line
	}
	padding := max(width-codextui.DisplayWidth(line), 0)
	return prefix + line + strings.Repeat(" ", padding) + "\x1b[0m"
}

// sidebarDocumentLinkSpan finds the destination span of a rendered link and
// widens it over the escape sequence that introduces it, so a style applied to
// the span replaces the link color instead of nesting inside it.
func sidebarDocumentLinkSpan(line string, path string) (int, int, bool) {
	if path == "" {
		return 0, 0, false
	}
	index := strings.LastIndex(line, path)
	if index < 0 {
		return 0, 0, false
	}
	start := index
	for start > 0 && index-start <= 12 && line[start-1] != 'm' {
		start--
	}
	if start > 0 && line[start-1] == 'm' {
		if open := strings.LastIndex(line[:start], "\x1b["); open >= 0 {
			start = open
		}
	} else {
		start = index
	}
	return start, index + len(path), true
}

// highlightSidebarDocumentLink reverses the destination span of the selected
// link so Tab navigation shows which link Enter would open.
func highlightSidebarDocumentLink(line string, path string) string {
	start, end, ok := sidebarDocumentLinkSpan(line, path)
	if !ok || end <= start {
		return line
	}
	return line[:start] + "\x1b[7m" + line[start:end] + "\x1b[27m" + line[end:]
}

// boldSidebarDocumentLink emphasizes the link under the pointer (Rust
// TranscriptView::paint_document_links uses bold for the hovered destination).
func boldSidebarDocumentLink(line string, path string) string {
	start, end, ok := sidebarDocumentLinkSpan(line, path)
	if !ok || end <= start {
		return line
	}
	return line[:start] + "\x1b[1m" + line[start:end] + "\x1b[22m" + line[end:]
}
