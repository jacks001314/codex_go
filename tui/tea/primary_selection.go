package tea

import (
	"strings"
	"time"

	bubbletea "github.com/charmbracelet/bubbletea"
)

// PrimarySelectionReadResultMsg carries an asynchronous middle-click paste.
type PrimarySelectionReadResultMsg struct {
	RequestID uint64
	Text      string
	Err       error
}

// PrimarySelectionWriteResultMsg records whether a copy-on-select publication
// succeeded, so focus loss can release the helper-owned selection.
type PrimarySelectionWriteResultMsg struct {
	Published bool
}

func (m *Model) publishPrimarySelectionCmd(text string) bubbletea.Cmd {
	if m == nil || m.onPrimarySelectionWrite == nil || text == "" {
		return nil
	}
	writer := m.onPrimarySelectionWrite
	if m.clipboardWorker == nil {
		m.clipboardWorker = newClipboardWorker()
	}
	result, err := m.clipboardWorker.queuePrimary(text, writer)
	if err != nil {
		return func() bubbletea.Msg { return PrimarySelectionWriteResultMsg{} }
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
			return PrimarySelectionWriteResultMsg{Published: err == nil}
		case <-timer.C:
			return PrimarySelectionWriteResultMsg{}
		}
	}
}

func (m *Model) applyPrimarySelectionWriteResult(msg PrimarySelectionWriteResultMsg) {
	if m != nil && msg.Published {
		m.primarySelectionPublished = true
	}
}

func (m *Model) releasePrimarySelectionCmd() bubbletea.Cmd {
	if m == nil || !m.primarySelectionPublished || m.onPrimarySelectionRelease == nil {
		return nil
	}
	m.primarySelectionPublished = false
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
	release := m.onPrimarySelectionRelease
	go func() {
		_ = release()
	}()
}

// startPrimarySelectionPaste starts the X11 PRIMARY read for an unmodified
// middle-button press (Rust #49112). The result is inserted only when no
// interaction surface took ownership meanwhile.
func (m *Model) startPrimarySelectionPaste(msg bubbletea.MouseMsg) (bool, bubbletea.Cmd) {
	if m == nil || m.onPrimarySelectionRead == nil ||
		msg.Action != bubbletea.MouseActionPress ||
		msg.Button != bubbletea.MouseButtonMiddle ||
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
