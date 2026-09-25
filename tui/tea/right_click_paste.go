package tea

import (
	"errors"
	"os"
	"runtime"

	bubbletea "github.com/charmbracelet/bubbletea"

	codextui "codex_go/tui"
	tuiapp "codex_go/tui/app"
	tuiinternal "codex_go/tui/tui"
)

// Rust parity: codex-rs/tui/src/app/right_click_paste.rs (#48118). The
// fullscreen TUI falls back to pasting clipboard text on an unmodified
// right-button press that no selection, search or blocking view claims.
//
// Go's fullscreen (alt-screen) program deliberately leaves terminal mouse
// tracking off so the terminal keeps its own selection/copy and native paste,
// where Rust's owned screen installs EnableMouseCapture and therefore implements
// the fallback itself. The fallback below is the aligned behavior for any host
// or test that does report the press (terminals with native right-click paste
// already produce the same result for `auto`), and it honors `off`, SSH and
// recognized VS Code terminals.

type rightClickPasteReadMsg struct {
	text string
	err  error
}

type rightClickPasteResult struct {
	text string
	err  string
}

func detectRightClickPasteEnvironment() tuiapp.PasteEnvironment {
	env := map[string]string{
		"SSH_TTY":        os.Getenv("SSH_TTY"),
		"SSH_CONNECTION": os.Getenv("SSH_CONNECTION"),
	}
	return tuiapp.DetectPasteEnvironment(runtime.GOOS, env, codextui.IsProbablyWSL(), tuiinternal.DetectVscodeTerminal())
}

// setRightClickPasteMode applies a resolved `tui.right_click_paste` value,
// mirroring Rust local_settings.tui.right_click_paste.
func (m *Model) setRightClickPasteMode(raw string) {
	if m == nil {
		return
	}
	m.rightClickPasteMode = tuiapp.ParseRightClickPasteMode(raw)
}

// rightClickPasteTargetFor mirrors ChatWidget::right_click_paste_target: the
// editable composer snapshot the pending read is bound to.
func (m *Model) rightClickPasteTargetFor() (tuiapp.RightClickPasteTarget, bool) {
	if m == nil {
		return tuiapp.RightClickPasteTarget{}, false
	}
	return tuiapp.RightClickPasteTargetFor(tuiapp.RightClickPasteTargetInput{
		// The fullscreen surface is the alt-screen program (Rust's owned screen).
		OwnedScreen:      !m.noAltScreen,
		OverlayActive:    m.overlay != nil || m.modal != nil || m.agentsOverview != nil || m.readOnlyThread,
		ComposerEditable: m.composerCanPasteOnRightClick(),
		Thread:           m.currentThreadID(),
		Draft:            m.composer.Value(),
		Cursor:           m.composer.LineInfo().StartColumn,
	}, m.rightClickPasteEnv, m.rightClickPasteMode)
}

// composerCanPasteOnRightClick mirrors BottomPane::can_paste_on_right_click and
// ChatComposer::can_paste_on_right_click. Go has no composer-owned history
// search or mouse selection (the terminal owns selection), so the blocking views
// and the vim search query are the gates.
func (m *Model) composerCanPasteOnRightClick() bool {
	if m == nil || m.modal != nil || m.overlay != nil || m.agentsOverview != nil {
		return false
	}
	return !m.vimSearchMode
}

// startRightClickPaste mirrors App::start_right_click_paste: an unmodified
// right-button press with no pending read and an idle worker starts an
// asynchronous clipboard read bound to the current composer target.
func (m *Model) startRightClickPaste(msg bubbletea.MouseMsg) bubbletea.Cmd {
	if m == nil {
		return nil
	}
	rightButtonDown := msg.Action == bubbletea.MouseActionPress && msg.Button == bubbletea.MouseButtonRight
	// Bubble Tea v1 mouse messages carry no modifier state, so the press is
	// treated as unmodified; the eligible-by-default branch is the one Rust
	// enables for `auto`.
	if !tuiapp.ShouldStartRightClickPaste(rightButtonDown, true, m.rightClickPasteTarget != nil, m.clipboardReadPending) {
		return nil
	}
	target, ok := m.rightClickPasteTargetFor()
	if !ok {
		return nil
	}
	m.rightClickPasteTarget = &target
	m.rightClickPasteResult = nil
	m.clipboardReadPending = true
	read := m.clipboardRead
	return func() bubbletea.Msg {
		text, err := tuiapp.ReadClipboardTextWithDeadline(read, tuiapp.RightClickPasteReadTimeout)
		return rightClickPasteReadMsg{text: text, err: err}
	}
}

// applyRightClickPasteRead records the worker's result. Rust delivers the read
// only on a draw, so the result waits for the next render instead of replacing
// input that may still be queued.
func (m *Model) applyRightClickPasteRead(msg rightClickPasteReadMsg) {
	if m == nil {
		return
	}
	m.clipboardReadPending = false
	if m.rightClickPasteTarget == nil {
		return
	}
	result := &rightClickPasteResult{}
	switch {
	case errors.Is(msg.err, tuiapp.ErrRightClickPasteReadTimeout):
		result.err = "clipboard read timed out"
	case msg.err != nil:
		result.err = "clipboard text is unavailable"
	default:
		text, err := tuiapp.ValidateRightClickPasteText(msg.text, false)
		result.text = text
		result.err = err
	}
	m.rightClickPasteResult = result
}

// invalidateRightClickPaste mirrors App::invalidate_right_click_paste: real
// input, a bracketed paste, focus loss or a resume discards the pending read so
// a late clipboard result can never overwrite newer input.
func (m *Model) invalidateRightClickPaste(event tuiapp.RightClickPasteEvent) {
	if m == nil || tuiapp.RightClickPasteKeepsPending(event) {
		return
	}
	m.rightClickPasteTarget = nil
	m.rightClickPasteResult = nil
}

// rightClickPasteEventFor maps a Bubble Tea message onto Rust's TuiEvent
// invalidation rule. Messages that are not app events (async results, timers)
// leave the pending read alone.
func rightClickPasteEventFor(message bubbletea.Msg) (tuiapp.RightClickPasteEvent, bool) {
	switch msg := message.(type) {
	case bubbletea.KeyMsg:
		if msg.Paste {
			return tuiapp.RightClickPasteEvent{Kind: tuiapp.RightClickPasteEventPaste}, true
		}
		return tuiapp.RightClickPasteEvent{Kind: tuiapp.RightClickPasteEventKey}, true
	case bubbletea.MouseMsg:
		kind := tuiapp.RightClickPasteMouseOther
		switch {
		case msg.Action == bubbletea.MouseActionMotion:
			kind = tuiapp.RightClickPasteMouseMoved
		case msg.Action == bubbletea.MouseActionRelease && msg.Button == bubbletea.MouseButtonRight:
			kind = tuiapp.RightClickPasteMouseUpRight
		case msg.Action == bubbletea.MouseActionPress && msg.Button == bubbletea.MouseButtonRight:
			kind = tuiapp.RightClickPasteMouseDownRight
		}
		// Bubble Tea v1 reports no mouse modifiers, so a right-button press is
		// treated as the unmodified press Rust keeps.
		return tuiapp.RightClickPasteEvent{Kind: tuiapp.RightClickPasteEventMouse, MouseKind: kind, ModifiersEmpty: true}, true
	case bubbletea.FocusMsg:
		return tuiapp.RightClickPasteEvent{Kind: tuiapp.RightClickPasteEventFocusGained}, true
	case bubbletea.BlurMsg:
		return tuiapp.RightClickPasteEvent{Kind: tuiapp.RightClickPasteEventFocusLost}, true
	case bubbletea.WindowSizeMsg:
		return tuiapp.RightClickPasteEvent{Kind: tuiapp.RightClickPasteEventResize}, true
	default:
		return tuiapp.RightClickPasteEvent{}, false
	}
}

// resetRightClickPaste drops the pending read and its result, mirroring Rust
// App::replace_chat_widget (a replaced composer invalidates the bound target).
func (m *Model) resetRightClickPaste() {
	if m == nil {
		return
	}
	m.rightClickPasteTarget = nil
	m.rightClickPasteResult = nil
	m.clipboardReadPending = false
}

// deliverRightClickPasteAtRender mirrors the draw half of
// App::finish_right_click_paste: the stored read is delivered only while the
// same thread, draft and cursor remain eligible, and an empty read is dropped.
func (m *Model) deliverRightClickPasteAtRender() {
	if m == nil {
		return
	}
	result := m.rightClickPasteResult
	pending := m.rightClickPasteTarget
	m.rightClickPasteResult = nil
	m.rightClickPasteTarget = nil
	if result == nil || pending == nil {
		return
	}
	current, currentOK := m.rightClickPasteTargetFor()
	decision := tuiapp.RightClickPasteDeliveryFor(*pending, true, current, currentOK, result.text, result.err)
	switch decision.Kind {
	case tuiapp.RightClickPasteDeliveryPaste:
		m.composer.InsertString(decision.Text)
		m.extendComposerPasteWindow(m.currentTime())
		m.refreshSlashPopup()
	case tuiapp.RightClickPasteDeliveryError:
		m.notice = decision.Error
	}
}
