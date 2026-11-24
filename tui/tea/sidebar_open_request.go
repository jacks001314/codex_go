package tea

import (
	"strconv"
	"strings"
	"time"

	bubbletea "charm.land/bubbletea/v2"

	"codex_go/tui/markdown"
)

// Rust #63184: the terminal-side `open_in_codex` tool shows a workspace file
// next to the conversation. Requests queue until the calling thread is the
// visible one, and showing the preview never takes keyboard focus from the
// chat.

// SidebarOpenRequestMsg asks the TUI to show one file for a thread.
type SidebarOpenRequestMsg struct {
	SourceThreadID string
	Path           string
	// Line is the optional one-based line to reveal; zero shows the file from
	// the top.
	Line int
}

type sidebarOpenRequest struct {
	Path string
	Line int
}

// applySidebarOpenRequest opens the file now when its thread is visible and
// queues it otherwise. A request without a calling thread is dropped: the tool
// may only show files for the conversation that asked.
func (m *Model) applySidebarOpenRequest(msg SidebarOpenRequestMsg) bubbletea.Cmd {
	if m == nil || m.onSidebarDocument == nil {
		return nil
	}
	path := strings.TrimSpace(msg.Path)
	source := strings.TrimSpace(msg.SourceThreadID)
	if path == "" || source == "" {
		return nil
	}
	request := sidebarOpenRequest{Path: path, Line: max(msg.Line, 0)}
	if source != m.currentThreadID() {
		if m.pendingSidebarOpens == nil {
			m.pendingSidebarOpens = map[string][]sidebarOpenRequest{}
		}
		m.pendingSidebarOpens[source] = append(m.pendingSidebarOpens[source], request)
		return nil
	}
	return m.openModelSidebarDocument(source, request)
}

// openModelSidebarDocument opens one preview tab for the visible thread. It
// reuses a loaded tab for the same path and replaces a failed one, like a
// chooser row, but leaves the sidebar's focus where it was.
func (m *Model) openModelSidebarDocument(source string, request sidebarOpenRequest) bubbletea.Cmd {
	parent := m.currentThreadID()
	if m == nil || m.onSidebarDocument == nil || parent == "" || strings.TrimSpace(request.Path) == "" {
		return nil
	}
	var location *markdown.SidebarSourceLocation
	if request.Line > 0 {
		location = &markdown.SidebarSourceLocation{Line: request.Line, Column: 1}
	}

	for _, tab := range m.sidebarDocumentsFor(parent) {
		if tab == nil || strings.TrimSpace(tab.Path) != request.Path {
			continue
		}
		if tab.Loading {
			m.sidebarDocumentActive = tab.ID
			m.refreshTranscript()
			return nil
		}
		if tab.View.Err != "" {
			m.closeSidebarDocumentTab(tab.ID)
			break
		}
		focused := m.sidebarChooserFocused
		cmd := m.reloadSidebarDocument(tab, -1, location)
		// Reloading focuses the pane in the chooser path; a model request keeps
		// the chat's keyboard focus (Rust #63184).
		m.sidebarChooserFocused = focused
		return cmd
	}
	m.sidebarDocumentCounter++
	m.recordSidebarOpenedFile(parent, request.Path)
	id := "document-" + strconv.FormatUint(m.sidebarDocumentCounter, 10)
	tab := &sidebarDocumentTab{
		ID:        id,
		Parent:    parent,
		Source:    source,
		Link:      -1,
		HoverLink: -1,
		Path:      request.Path,
		Location:  location,
		Loading:   true,
		Started:   time.Now(),
	}
	m.sidebarDocuments[parent] = append(m.sidebarDocuments[parent], tab)
	m.sidebarDocumentActive = id
	m.sidebarTabHover = -1
	m.sidebarAddHover = false
	m.refreshTranscript()
	open := m.onSidebarDocument
	docRequest := SidebarDocumentRequest{
		ParentThreadID: parent,
		SourceThreadID: source,
		Path:           request.Path,
		Link:           -1,
		Location:       location,
	}
	return func() bubbletea.Msg {
		return SidebarDocumentLoadedMsg{ParentThreadID: parent, ID: id, View: open(docRequest)}
	}
}

// drainPendingSidebarOpens shows the requests queued for the visible thread
// (Rust #63184 opens them when returning to the calling conversation).
func (m *Model) drainPendingSidebarOpens() bubbletea.Cmd {
	if m == nil || len(m.pendingSidebarOpens) == 0 {
		return nil
	}
	threadID := m.currentThreadID()
	pending := m.pendingSidebarOpens[threadID]
	if threadID == "" || len(pending) == 0 {
		return nil
	}
	delete(m.pendingSidebarOpens, threadID)
	cmds := make([]bubbletea.Cmd, 0, len(pending))
	for _, request := range pending {
		if cmd := m.openModelSidebarDocument(threadID, request); cmd != nil {
			cmds = append(cmds, cmd)
		}
	}
	if len(cmds) == 0 {
		return nil
	}
	return bubbletea.Batch(cmds...)
}
