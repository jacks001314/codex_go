package tea

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	bubbletea "charm.land/bubbletea/v2"

	codextui "codex_go/tui"
	"codex_go/tui/keymsg"
	"codex_go/tui/markdown"
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
	Link    int
	Path    string
	View    SidebarDocumentView
	Loading bool
	// Started anchors the loading glyph (Rust State::Loading(Instant)).
	Started time.Time
	Scroll  int

	renderWidth int
	renderLines []string
	rendered    bool
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
	if m == nil || m.onSidebarDocument == nil {
		return nil
	}
	parent := strings.TrimSpace(m.sidebarChooserParent)
	if parent == "" {
		parent = m.sidebarParentThreadID()
	}
	path := strings.TrimSpace(link.Path)
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
		m.sidebarDocumentActive = tab.ID
		m.sidebarChooserFocused = true
		m.sidebarTabHover = -1
		m.refreshTranscript()
		return nil
	}
	m.sidebarDocumentCounter++
	id := "document-" + strconv.FormatUint(m.sidebarDocumentCounter, 10)
	tab := &sidebarDocumentTab{
		ID:      id,
		Parent:  parent,
		Source:  parent,
		Link:    -1,
		Path:    path,
		Loading: true,
		Started: time.Now(),
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
		Link:           -1,
	}
	return func() bubbletea.Msg {
		return SidebarDocumentLoadedMsg{ParentThreadID: parent, ID: id, View: open(request)}
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
	action := sidebarChooserListAction(msg)
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
		m.suppressCurrentViewKeyUntilRelease()
		return true, m.closeSidebarDocumentTab(tab.ID)
	case "accept", "move_left", "move_right":
		// Rust keeps the tab shortcut on Left/Right and Enter for links; links
		// arrive with the document link pass, so consume the key for now.
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
	for index := 0; index < contentHeight && tab.Scroll+index < len(lines); index++ {
		rows[1+index] = " " + codextui.TruncateToWidth(lines[tab.Scroll+index], bodyWidth)
	}
	rows[footer] = " " + m.sidebarDocumentFooter(bodyWidth, tab)
	return strings.Join(rows, "\n")
}

// sidebarDocumentFooter mirrors Rust's reader hints: read, find, link, select,
// close. The Go pane only wires reading and closing so far.
func (m *Model) sidebarDocumentFooter(width int, tab *sidebarDocumentTab) string {
	if m == nil || tab == nil {
		return ""
	}
	hints := make([]string, 0, 2)
	up := m.sidebarChooserListHint("move_up")
	down := m.sidebarChooserListHint("move_down")
	if up != "" && down != "" {
		hints = append(hints, up+"/"+down+" read")
	}
	if key := m.sidebarChooserListHint("cancel"); key != "" {
		hints = append(hints, key+" close")
	}
	return fitSidebarChooserHints(hints, width)
}
