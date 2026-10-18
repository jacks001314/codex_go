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

func (m *Model) publishPrimarySelectionCmd(text string) bubbletea.Cmd {
	if m == nil || m.onPrimarySelectionWrite == nil || text == "" {
		return nil
	}
	writer := m.onPrimarySelectionWrite
	return func() bubbletea.Msg {
		_ = writer(text)
		return nil
	}
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
