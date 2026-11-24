package tea

import (
	mousemsg "codex_go/tui/mousemsg"
	"strings"
	"time"

	bubbletea "charm.land/bubbletea/v2"
)

// PrimarySelectionReadResultMsg carries an asynchronous middle-click paste.
type PrimarySelectionReadResultMsg struct {
	RequestID uint64
	Text      string
	Err       error
}

// PrimarySelectionWriteResultMsg records whether a copy-on-select publication
// succeeded, so focus loss can release the helper-owned selection. DocumentID
// names the preview that asked, so hiding that preview can release the claim.
type PrimarySelectionWriteResultMsg struct {
	Published  bool
	DocumentID string
}

func (m *Model) publishPrimarySelectionCmd(text string) bubbletea.Cmd {
	return m.publishPrimarySelectionCmdFor("", text)
}

// publishSidebarDocumentPrimaryCmd publishes a preview selection; the result
// message names the tab so a confirmed publication records its owner (Rust
// Selection::primary_owner).
func (m *Model) publishSidebarDocumentPrimaryCmd(tab *sidebarDocumentTab, text string) bubbletea.Cmd {
	if m == nil || tab == nil {
		return nil
	}
	return m.publishPrimarySelectionCmdFor(tab.ID, text)
}

func (m *Model) publishPrimarySelectionCmdFor(documentID string, text string) bubbletea.Cmd {
	if m == nil || m.onPrimarySelectionWrite == nil || text == "" {
		return nil
	}
	writer := m.onPrimarySelectionWrite
	if m.clipboardWorker == nil {
		m.clipboardWorker = newClipboardWorker()
	}
	result, err := m.clipboardWorker.queuePrimary(text, writer)
	if err != nil {
		return func() bubbletea.Msg { return PrimarySelectionWriteResultMsg{DocumentID: documentID} }
	}
	timeout := m.clipboardSetupTimeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	return func() bubbletea.Msg {
		timer := time.NewTimer(timeout)
		defer timer.Stop()
		select {
		case err := <-result:
			return PrimarySelectionWriteResultMsg{Published: err == nil, DocumentID: documentID}
		case <-timer.C:
			return PrimarySelectionWriteResultMsg{DocumentID: documentID}
		}
	}
}

func (m *Model) applyPrimarySelectionWriteResult(msg PrimarySelectionWriteResultMsg) {
	if m == nil || !msg.Published {
		return
	}
	m.primarySelectionPublished = true
	if msg.DocumentID == "" {
		// Another surface published last; the previews no longer own PRIMARY.
		m.primarySelectionDocument = ""
		m.clearSidebarDocumentPrimary()
		return
	}
	m.primarySelectionDocument = msg.DocumentID
	tab := m.sidebarDocumentTab(msg.DocumentID)
	if tab == nil {
		// The preview closed while the publication was queued (Rust drops the
		// owner with the document).
		m.releasePrimarySelectionOwnership()
		return
	}
	tab.primaryPublished = true
	if !m.sidebarDocumentVisibleOwns(tab) {
		m.cancelHiddenSidebarDocumentPrimary()
	}
}

// sidebarDocumentTab finds an open preview by id across every sidebar parent.
func (m *Model) sidebarDocumentTab(id string) *sidebarDocumentTab {
	if m == nil || id == "" {
		return nil
	}
	for _, tabs := range m.sidebarDocuments {
		for _, tab := range tabs {
			if tab != nil && tab.ID == id {
				return tab
			}
		}
	}
	return nil
}

// sidebarDocumentVisibleOwns reports whether the pane is painting the tab.
func (m *Model) sidebarDocumentVisibleOwns(tab *sidebarDocumentTab) bool {
	if m == nil || tab == nil || !m.sidebarDocumentVisible() {
		return false
	}
	return m.sidebarDocumentActiveTab() == tab
}

// cancelHiddenSidebarDocumentPrimary clears a preview's copy-on-select PRIMARY
// claim once the pane stops showing that tab (Rust
// Sidebar::cancel_hidden_document_primary). The release runs off the update
// loop like releasePrimarySelectionOwnership, and it only fires when the
// preview is the surface that published the current PRIMARY value.
func (m *Model) cancelHiddenSidebarDocumentPrimary() {
	if m == nil {
		return
	}
	visible := m.sidebarDocumentVisible()
	var active *sidebarDocumentTab
	if visible {
		active = m.sidebarDocumentActiveTab()
	}
	for _, tabs := range m.sidebarDocuments {
		for _, tab := range tabs {
			if tab == nil || !tab.primaryPublished || tab == active {
				continue
			}
			tab.primaryPublished = false
			if m.primarySelectionDocument == tab.ID {
				m.releasePrimarySelectionOwnership()
			}
		}
	}
}

// clearSidebarDocumentPrimary drops every preview's PRIMARY claim without
// releasing the shared selection, used when another surface takes it over.
func (m *Model) clearSidebarDocumentPrimary() {
	if m == nil {
		return
	}
	for _, tabs := range m.sidebarDocuments {
		for _, tab := range tabs {
			if tab != nil {
				tab.primaryPublished = false
			}
		}
	}
}

func (m *Model) releasePrimarySelectionCmd() bubbletea.Cmd {
	if m == nil || !m.primarySelectionPublished || m.onPrimarySelectionRelease == nil {
		return nil
	}
	m.primarySelectionPublished = false
	m.primarySelectionDocument = ""
	m.clearSidebarDocumentPrimary()
	release := m.onPrimarySelectionRelease
	return func() bubbletea.Msg {
		_ = release()
		return nil
	}
}

// releasePrimarySelectionOwnership releases helper-owned PRIMARY state on a
// thread resume without blocking the update loop (Rust #53753).
func (m *Model) releasePrimarySelectionOwnership() {
	if m == nil || !m.primarySelectionPublished || m.onPrimarySelectionRelease == nil {
		return
	}
	m.primarySelectionPublished = false
	m.primarySelectionDocument = ""
	m.clearSidebarDocumentPrimary()
	release := m.onPrimarySelectionRelease
	go func() {
		_ = release()
	}()
}

// startPrimarySelectionPaste starts the X11 PRIMARY read for an unmodified
// middle-button press (Rust #49112). The result is inserted only when no
// interaction surface took ownership meanwhile.
func (m *Model) startPrimarySelectionPaste(msg mousemsg.MouseMsg) (bool, bubbletea.Cmd) {
	if m == nil || m.onPrimarySelectionRead == nil ||
		msg.Action != mousemsg.MouseActionPress ||
		msg.Button != mousemsg.MouseButtonMiddle ||
		msg.Ctrl || msg.Alt || msg.Shift ||
		m.modal != nil || m.slashPopup.Active || m.skillPopup.Active || m.mentionPopup != nil ||
		m.readOnlyThread {
		return false, nil
	}
	reader := m.onPrimarySelectionRead
	m.nextPrimarySelectionReadID++
	if m.nextPrimarySelectionReadID == 0 {
		m.nextPrimarySelectionReadID = 1
	}
	requestID := m.nextPrimarySelectionReadID
	m.pendingPrimarySelectionRead = requestID
	return true, func() bubbletea.Msg {
		text, err := reader()
		return PrimarySelectionReadResultMsg{RequestID: requestID, Text: text, Err: err}
	}
}

func (m *Model) applyPrimarySelectionRead(msg PrimarySelectionReadResultMsg) {
	if m == nil || msg.RequestID == 0 || msg.RequestID != m.pendingPrimarySelectionRead {
		return
	}
	m.pendingPrimarySelectionRead = 0
	if msg.Err != nil || strings.TrimSpace(msg.Text) == "" {
		return
	}
	if m.modal != nil || m.slashPopup.Active || m.skillPopup.Active || m.mentionPopup != nil || m.readOnlyThread {
		return
	}
	m.insertComposerPaste(msg.Text)
}
