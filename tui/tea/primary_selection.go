package tea

import (
	"strings"

	bubbletea "github.com/charmbracelet/bubbletea"
)

// PrimarySelectionReadResultMsg carries an asynchronous middle-click paste.
type PrimarySelectionReadResultMsg struct {
	Text string
	Err  error
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
	return func() bubbletea.Msg {
		return PrimarySelectionWriteResultMsg{Published: writer(text) == nil}
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
	return true, func() bubbletea.Msg {
		text, err := reader()
		return PrimarySelectionReadResultMsg{Text: text, Err: err}
	}
}

func (m *Model) applyPrimarySelectionRead(msg PrimarySelectionReadResultMsg) {
	if m == nil || msg.Err != nil || strings.TrimSpace(msg.Text) == "" {
		return
	}
	if m.modal != nil || m.slashPopup.Active || m.skillPopup.Active || m.mentionPopup != nil || m.readOnlyThread {
		return
	}
	m.insertComposerPaste(msg.Text)
}
