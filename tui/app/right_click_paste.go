package app

import (
	"errors"
	"runtime"
	"strings"
	"time"
	"unicode/utf8"

	tuiinternal "codex_go/tui/tui"
)

// Rust parity subset: codex-rs/tui/src/app/right_click_paste.rs (#48118). The
// fullscreen TUI falls back to pasting the clipboard text on a right click when
// no selection, search or blocking view owns the input.

// RightClickPasteMode is the `tui.right_click_paste` value. Rust deserializes it
// strictly (an unknown spelling fails the config load); Go's `[tui]` sub-table
// is not value-validated, so the resolver falls back to `auto`.
type RightClickPasteMode string

const (
	RightClickPasteAuto RightClickPasteMode = "auto"
	RightClickPasteOn   RightClickPasteMode = "on"
	RightClickPasteOff  RightClickPasteMode = "off"
)

// ParseRightClickPasteMode resolves a configured value, defaulting to `auto`
// exactly like Rust's `#[serde(default)]` enum.
func ParseRightClickPasteMode(raw string) RightClickPasteMode {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case string(RightClickPasteOn):
		return RightClickPasteOn
	case string(RightClickPasteOff):
		return RightClickPasteOff
	default:
		return RightClickPasteAuto
	}
}

// PasteEnvironment is Rust's PasteEnvironment: the host and terminal facts the
// policy is decided from.
type PasteEnvironment struct {
	PlatformDefault bool
	SSH             bool
	WSL             bool
	Vscode          tuiinternal.VscodeDetection
}

// DetectPasteEnvironment mirrors Rust PasteEnvironment::detect. The caller
// supplies the environment, host OS and WSL/VS Code readings because Go resolves
// them from its own seams.
func DetectPasteEnvironment(goos string, env map[string]string, wsl bool, vscode tuiinternal.VscodeDetection) PasteEnvironment {
	return PasteEnvironment{
		PlatformDefault: goos == "windows" || goos == "linux",
		SSH:             strings.TrimSpace(env["SSH_TTY"]) != "" || strings.TrimSpace(env["SSH_CONNECTION"]) != "",
		WSL:             wsl,
		Vscode:          vscode,
	}
}

// Allows mirrors Rust PasteEnvironment::allows. SSH sessions and recognized VS
// Code terminals always win over the configured mode.
func (e PasteEnvironment) Allows(mode RightClickPasteMode) bool {
	if e.SSH || e.Vscode == tuiinternal.VscodeDetectionVsCode {
		return false
	}
	switch mode {
	case RightClickPasteOff:
		return false
	case RightClickPasteOn:
		return runtime.GOOS != "android"
	default: // auto
		return e.PlatformDefault && !(e.WSL && e.Vscode == tuiinternal.VscodeDetectionUnknown)
	}
}

// RightClickPasteTarget is the editable composer snapshot a pending read is
// bound to. Rust compares `(composer_text, composer_cursor)` plus the displayed
// thread id.
type RightClickPasteTarget struct {
	Thread string
	Draft  string
	Cursor int
}

// RightClickPasteTargetInput collects the gates Rust reads from the app and the
// chat widget before a read may start.
type RightClickPasteTargetInput struct {
	OwnedScreen      bool
	OverlayActive    bool
	HasSelection     bool
	SearchActive     bool
	ComposerEditable bool
	Thread           string
	Draft            string
	Cursor           int
}

// RightClickPasteTargetFor mirrors Rust App::right_click_paste_target and
// ChatWidget::right_click_paste_target.
func RightClickPasteTargetFor(in RightClickPasteTargetInput, env PasteEnvironment, mode RightClickPasteMode) (RightClickPasteTarget, bool) {
	if !in.OwnedScreen || in.OverlayActive || in.HasSelection || in.SearchActive || !env.Allows(mode) {
		return RightClickPasteTarget{}, false
	}
	if !in.ComposerEditable {
		return RightClickPasteTarget{}, false
	}
	return RightClickPasteTarget{Thread: in.Thread, Draft: in.Draft, Cursor: in.Cursor}, true
}

// ShouldStartRightClickPaste mirrors Rust App::start_right_click_paste's guard:
// an unmodified right-button press with no pending read and an idle clipboard
// worker.
func ShouldStartRightClickPaste(rightButtonDown bool, modifiersEmpty bool, pending bool, clipboardBusy bool) bool {
	return rightButtonDown && modifiersEmpty && !pending && !clipboardBusy
}

// RightClickPasteEventKind enumerates the app events Rust's invalidation rule
// distinguishes.
type RightClickPasteEventKind string

const (
	RightClickPasteEventDraw        RightClickPasteEventKind = "draw"
	RightClickPasteEventResize      RightClickPasteEventKind = "resize"
	RightClickPasteEventFocusGained RightClickPasteEventKind = "focus_gained"
	RightClickPasteEventFocusLost   RightClickPasteEventKind = "focus_lost"
	RightClickPasteEventKey         RightClickPasteEventKind = "key"
	RightClickPasteEventPaste       RightClickPasteEventKind = "paste"
	RightClickPasteEventResume      RightClickPasteEventKind = "resume"
	RightClickPasteEventMouse       RightClickPasteEventKind = "mouse"
)

// RightClickPasteMouseKind distinguishes the mouse events Rust's rule keeps.
type RightClickPasteMouseKind string

const (
	RightClickPasteMouseMoved     RightClickPasteMouseKind = "moved"
	RightClickPasteMouseDownRight RightClickPasteMouseKind = "down_right"
	RightClickPasteMouseUpRight   RightClickPasteMouseKind = "up_right"
	RightClickPasteMouseOther     RightClickPasteMouseKind = "other"
)

// RightClickPasteEvent is one app event as the invalidation rule sees it.
type RightClickPasteEvent struct {
	Kind           RightClickPasteEventKind
	MouseKind      RightClickPasteMouseKind
	ModifiersEmpty bool
}

// RightClickPasteKeepsPending mirrors Rust App::invalidate_right_click_paste: a
// draw, resize, focus gain, a mouse move, a right-button release, or the
// originating unmodified right-button press keep the pending read; every other
// event (input, a bracketed paste, focus loss or resume) discards it so a late
// clipboard result can never overwrite newer input.
func RightClickPasteKeepsPending(event RightClickPasteEvent) bool {
	switch event.Kind {
	case RightClickPasteEventDraw, RightClickPasteEventResize, RightClickPasteEventFocusGained:
		return true
	case RightClickPasteEventMouse:
		switch event.MouseKind {
		case RightClickPasteMouseMoved, RightClickPasteMouseUpRight:
			return true
		case RightClickPasteMouseDownRight:
			return event.ModifiersEmpty
		default:
			return false
		}
	default: // key, paste, focus lost, resume
		return false
	}
}

// RightClickPasteDeliveryKind is Rust App::finish_right_click_paste's outcome.
type RightClickPasteDeliveryKind string

const (
	RightClickPasteDeliveryNone  RightClickPasteDeliveryKind = "none"
	RightClickPasteDeliveryPaste RightClickPasteDeliveryKind = "paste"
	RightClickPasteDeliveryError RightClickPasteDeliveryKind = "error"
)

// RightClickPasteDelivery is the decision to deliver a completed clipboard read.
type RightClickPasteDelivery struct {
	Kind  RightClickPasteDeliveryKind
	Text  string
	Error string
}

// RightClickPasteDeliveryFor mirrors Rust App::finish_right_click_paste: the
// read is only delivered on a draw, only while the pending target still matches
// the current thread, draft and cursor, and an empty read is ignored.
func RightClickPasteDeliveryFor(
	pending RightClickPasteTarget,
	pendingOK bool,
	current RightClickPasteTarget,
	currentOK bool,
	text string,
	readErr string,
) RightClickPasteDelivery {
	if !pendingOK || !currentOK || pending != current {
		return RightClickPasteDelivery{Kind: RightClickPasteDeliveryNone}
	}
	if readErr != "" {
		return RightClickPasteDelivery{Kind: RightClickPasteDeliveryError, Error: readErr}
	}
	if text == "" {
		return RightClickPasteDelivery{Kind: RightClickPasteDeliveryNone}
	}
	return RightClickPasteDelivery{Kind: RightClickPasteDeliveryPaste, Text: text}
}

const (
	// RightClickPasteReadTimeout is Rust's five-second clipboard read deadline.
	RightClickPasteReadTimeout = 5 * time.Second
	// RightClickPasteMaxTextChars mirrors `MAX_USER_INPUT_TEXT_CHARS`
	// (codex-rs/protocol/src/user_input.rs).
	RightClickPasteMaxTextChars = 1 << 20
	// RightClickPasteMaxTextBytes bounds the WSL reader's raw output before it is
	// validated as UTF-8, mirroring Rust's `MAX_USER_INPUT_TEXT_CHARS * 4`.
	RightClickPasteMaxTextBytes = RightClickPasteMaxTextChars * 4
)

// ValidateRightClickPasteText mirrors Rust clipboard_paste::text::validate.
func ValidateRightClickPasteText(text string, expired bool) (string, string) {
	switch {
	case expired:
		return "", "clipboard read timed out"
	case utf8.RuneCountInString(text) > RightClickPasteMaxTextChars:
		return "", "clipboard text exceeds the message size limit"
	default:
		return text, ""
	}
}

// ReadClipboardTextWithDeadline runs a clipboard read under Rust's five-second
// worker deadline. The reader runs off the caller's goroutine because the
// platform clipboard APIs are blocking; a read that outlives the deadline
// reports the timeout instead of blocking the update loop (Rust kills the
// reader and validates the deadline the same way).
func ReadClipboardTextWithDeadline(read func() (string, error), timeout time.Duration) (string, error) {
	if read == nil {
		return "", errRightClickPasteReaderUnavailable
	}
	if timeout <= 0 {
		timeout = RightClickPasteReadTimeout
	}
	type readResult struct {
		text string
		err  error
	}
	done := make(chan readResult, 1)
	go func() {
		text, err := read()
		done <- readResult{text: text, err: err}
	}()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case result := <-done:
		return result.text, result.err
	case <-timer.C:
		return "", ErrRightClickPasteReadTimeout
	}
}

// ErrRightClickPasteReadTimeout is the worker's deadline error.
var ErrRightClickPasteReadTimeout = errors.New("clipboard read timed out")

var errRightClickPasteReaderUnavailable = errors.New("clipboard text is unavailable")
