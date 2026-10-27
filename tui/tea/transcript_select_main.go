package tea

import (
	"strings"
	"time"

	bubbletea "github.com/charmbracelet/bubbletea"

	chatwidget "codex_go/tui/chatwidget"
)

// Rust parity: app/input/select_mode.rs and transcript_view/select_mode.rs
// (#57221-#57224). The owner resolves key bindings and hands the overlay a
// normalized SelectCommand so the pager itself stays keymap-free.

const selectExitGuardWindow = 500 * time.Millisecond

// beginTranscriptSelection opens the transcript pager with keyboard selection
// active (Rust `global.select_transcript`).
func (m *Model) beginTranscriptSelection() bubbletea.Cmd {
	if m == nil {
		return nil
	}
	if m.overlay == nil {
		cmd := m.openTranscriptOverlay()
		if m.overlay != nil {
			m.overlay.BeginSelection()
		}
		return cmd
	}
	m.overlay.BeginSelection()
	return nil
}

// updateTranscriptSelectionKey routes one key while selection mode owns the
// overlay. Every key is consumed so selection keystrokes never reach the
// composer draft (Rust #57224).
func (m *Model) updateTranscriptSelectionKey(msg bubbletea.KeyMsg) bubbletea.Cmd {
	if m == nil || m.overlay == nil || !m.overlay.SelectionActive() {
		return nil
	}
	// Rust ignores pastes outside the search editor.
	if msg.Paste {
		return nil
	}
	keySpec := keySpecFromKeyMsg(msg)
	if m.keyMatches("global", "find_transcript", keySpec) {
		m.overlay.HandleSelectCommand(chatwidget.SelectCmdBeginFind)
		return nil
	}
	command, ok := m.overlaySelectCommand(msg, keySpec)
	if !ok {
		return nil
	}
	if isShiftArrow(msg.Type) && !m.overlay.SelectionMarked() {
		// Rust starts a character mark before a shift-arrow motion.
		m.overlay.HandleSelectCommand(chatwidget.SelectCmdMarkCharacter)
	}
	switch m.overlay.HandleSelectCommand(command) {
	case chatwidget.SelectActionExit:
		m.armSelectExitGuard(keySpec)
		return m.closeTranscriptOverlay()
	case chatwidget.SelectActionCopy:
		return m.copyOverlaySelection(false, keySpec)
	case chatwidget.SelectActionCopyAndFollow:
		return m.copyOverlaySelection(true, keySpec)
	}
	return nil
}

// overlaySelectCommand resolves a key to a selection command, giving fixed
// selection controls precedence over remapped pager shortcuts.
func (m *Model) overlaySelectCommand(msg bubbletea.KeyMsg, keySpec string) (chatwidget.SelectCommand, bool) {
	switch msg.Type {
	case bubbletea.KeyEsc:
		return chatwidget.SelectCmdClearMark, true
	case bubbletea.KeyEnter:
		return chatwidget.SelectCmdCopyAndFollow, true
	case bubbletea.KeyLeft:
		return chatwidget.SelectCmdLeft, true
	case bubbletea.KeyRight:
		return chatwidget.SelectCmdRight, true
	case bubbletea.KeyUp:
		return chatwidget.SelectCmdUp, true
	case bubbletea.KeyDown:
		return chatwidget.SelectCmdDown, true
	case bubbletea.KeyShiftLeft:
		return chatwidget.SelectCmdLeft, true
	case bubbletea.KeyShiftRight:
		return chatwidget.SelectCmdRight, true
	case bubbletea.KeyShiftUp:
		return chatwidget.SelectCmdUp, true
	case bubbletea.KeyShiftDown:
		return chatwidget.SelectCmdDown, true
	case bubbletea.KeyHome:
		return chatwidget.SelectCmdLineStart, true
	case bubbletea.KeyCtrlHome:
		return chatwidget.SelectCmdBeginning, true
	case bubbletea.KeyEnd:
		return chatwidget.SelectCmdLineEnd, true
	case bubbletea.KeyCtrlEnd:
		return chatwidget.SelectCmdEnd, true
	case bubbletea.KeyPgUp:
		return chatwidget.SelectCmdPageUp, true
	case bubbletea.KeyPgDown:
		return chatwidget.SelectCmdPageDown, true
	case bubbletea.KeyCtrlB:
		return chatwidget.SelectCmdPageUp, true
	case bubbletea.KeyCtrlF:
		return chatwidget.SelectCmdPageDown, true
	case bubbletea.KeyCtrlU:
		return chatwidget.SelectCmdHalfPageUp, true
	case bubbletea.KeyCtrlD:
		return chatwidget.SelectCmdHalfPageDown, true
	case bubbletea.KeySpace:
		return chatwidget.SelectCmdMarkCharacter, true
	case bubbletea.KeyRunes:
		if len(msg.Runes) != 1 {
			return chatwidget.SelectCmdNone, true
		}
		if msg.Alt {
			return chatwidget.SelectCmdNone, true
		}
		r := msg.Runes[0]
		switch r {
		case 'q':
			return chatwidget.SelectCmdExit, true
		case '/':
			return chatwidget.SelectCmdBeginFind, true
		case 'v':
			return chatwidget.SelectCmdMarkCharacter, true
		case 'V':
			return chatwidget.SelectCmdMarkLine, true
		case 'o':
			return chatwidget.SelectCmdSwapEnds, true
		case 'y':
			return chatwidget.SelectCmdCopy, true
		case 'n':
			return chatwidget.SelectCmdRepeatFindOlder, true
		case 'N':
			return chatwidget.SelectCmdRepeatFindNewer, true
		case 'h':
			return chatwidget.SelectCmdLeft, true
		case 'l':
			return chatwidget.SelectCmdRight, true
		case 'k':
			return chatwidget.SelectCmdUp, true
		case 'j':
			return chatwidget.SelectCmdDown, true
		case 'w':
			return chatwidget.SelectCmdWordForward, true
		case 'b':
			return chatwidget.SelectCmdWordBackward, true
		case 'e':
			return chatwidget.SelectCmdWordEnd, true
		case '0':
			return chatwidget.SelectCmdLineStart, true
		case '$':
			return chatwidget.SelectCmdLineEnd, true
		case 'g':
			return chatwidget.SelectCmdBeginning, true
		case 'G':
			return chatwidget.SelectCmdEnd, true
		}
	}
	// Configured pager motions still drive the caret (Rust pager_motion).
	for action, command := range map[string]chatwidget.SelectCommand{
		chatwidget.PagerScrollUp:     chatwidget.SelectCmdUp,
		chatwidget.PagerScrollDown:   chatwidget.SelectCmdDown,
		chatwidget.PagerPageUp:       chatwidget.SelectCmdPageUp,
		chatwidget.PagerPageDown:     chatwidget.SelectCmdPageDown,
		chatwidget.PagerHalfPageUp:   chatwidget.SelectCmdHalfPageUp,
		chatwidget.PagerHalfPageDown: chatwidget.SelectCmdHalfPageDown,
		chatwidget.PagerJumpTop:      chatwidget.SelectCmdBeginning,
		chatwidget.PagerJumpBottom:   chatwidget.SelectCmdEnd,
	} {
		if m.keyMatches("pager", action, keySpec) {
			return command, true
		}
	}
	return chatwidget.SelectCmdNone, true
}

func isShiftArrow(keyType bubbletea.KeyType) bool {
	switch keyType {
	case bubbletea.KeyShiftLeft, bubbletea.KeyShiftRight, bubbletea.KeyShiftUp, bubbletea.KeyShiftDown:
		return true
	default:
		return false
	}
}

// copyOverlaySelection writes the marked range to the clipboard. A follow copy
// closes the overlay only after the write is confirmed (Rust
// pending_follow_close).
func (m *Model) copyOverlaySelection(follow bool, keySpec string) bubbletea.Cmd {
	if m == nil || m.overlay == nil {
		return nil
	}
	text := m.overlay.SelectedText()
	if strings.TrimSpace(text) == "" {
		return nil
	}
	if m.clipboardWrite == nil {
		m.notice = "Copy failed: clipboard is unavailable"
		return nil
	}
	if follow {
		m.selectCopyClose = true
		m.selectCopyExitKey = keySpec
	}
	return m.clipboardWriteCmd("selection", text, false, false)
}

// finishSelectCopyClose closes a selection overlay once a copy-and-follow write
// is confirmed.
func (m *Model) finishSelectCopyClose(success bool) bubbletea.Cmd {
	if m == nil || !m.selectCopyClose {
		return nil
	}
	m.selectCopyClose = false
	if !success || m.overlay == nil {
		return nil
	}
	m.armSelectExitGuard(m.selectCopyExitKey)
	m.selectCopyExitKey = ""
	if m.overlay != nil {
		m.overlay.EndSelection()
	}
	return m.closeTranscriptOverlay()
}

// armSelectExitGuard consumes held repeats of the key that left selection mode
// so a repeated Enter/Esc/q cannot submit a preserved composer draft.
func (m *Model) armSelectExitGuard(keySpec string) {
	if m == nil {
		return
	}
	keySpec = strings.TrimSpace(keySpec)
	if keySpec == "" {
		m.selectExitGuardSpec = ""
		m.selectExitGuardUntil = time.Time{}
		return
	}
	m.selectExitGuardSpec = keySpec
	m.selectExitGuardUntil = m.currentTime().Add(selectExitGuardWindow)
}

func (m *Model) consumeSelectExitGuard(keySpec string) bool {
	if m == nil || m.selectExitGuardSpec == "" || strings.TrimSpace(keySpec) == "" {
		return false
	}
	if keySpec != m.selectExitGuardSpec {
		m.selectExitGuardSpec = ""
		m.selectExitGuardUntil = time.Time{}
		return false
	}
	now := m.currentTime()
	if !now.Before(m.selectExitGuardUntil) {
		m.selectExitGuardSpec = ""
		m.selectExitGuardUntil = time.Time{}
		return false
	}
	m.selectExitGuardUntil = now.Add(selectExitGuardWindow)
	return true
}
