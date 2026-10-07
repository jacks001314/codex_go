package tea

import (
	bubbletea "github.com/charmbracelet/bubbletea"

	codextui "codex_go/tui"
)

// Rust #50209 ("Make transcript mouse scroll speed configurable"): the transcript
// wheel moves one row per event, scaled by `tui.mouse_scroll_speed` (default
// 1.0, so `3.0` restores the old fixed three-row wheel). Fractional multipliers
// accumulate movement across events and drop the remainder when the direction
// reverses. Go's two transcript scroll surfaces — the main viewport and the
// fullscreen transcript pager — both consume this file: the pager through
// `chatwidget.TranscriptOverlay`, the main viewport through the helpers below.

// transcriptWheelRows maps one wheel event to the whole rows the main transcript
// should move, mirroring Rust `TranscriptView::handle_mouse`'s ScrollUp/
// ScrollDown arm (#50209). Wheel up scrolls toward earlier output (negative
// rows), wheel down toward later output.
func (m *Model) transcriptWheelRows(msg bubbletea.MouseMsg) (int, bool) {
	if m == nil || msg.Action != bubbletea.MouseActionPress {
		return 0, false
	}
	switch msg.Button {
	case bubbletea.MouseButtonWheelUp:
		return m.mouseScroll.Rows(-1), true
	case bubbletea.MouseButtonWheelDown:
		return m.mouseScroll.Rows(1), true
	default:
		return 0, false
	}
}

// scrollTranscriptRows applies whole-row wheel movement to the main transcript.
// Rust #50209 pauses following the latest output only when a whole row moves, so
// the follow flag is derived from the viewport after (not before) the move.
func (m *Model) scrollTranscriptRows(rows int) {
	if m == nil || rows == 0 {
		return
	}
	if rows < 0 {
		m.transcript.ScrollUp(-rows)
	} else {
		m.transcript.ScrollDown(rows)
	}
	m.activityFollow = m.transcript.AtBottom()
}

// setMouseScrollSpeed applies a resolved `tui.mouse_scroll_speed` to the live
// transcript surfaces. Rust threads the value into the transcript view and into
// `TranscriptOverlay::new` (#50209) and the resume picker's preview; Go has no
// session-preview scroll surface, so the main viewport and the fullscreen pager
// are the two consumers.
func (m *Model) setMouseScrollSpeed(speed float64) {
	if m == nil {
		return
	}
	m.mouseScroll.SetSpeed(speed)
	m.mouseScrollSpeed = m.mouseScroll.Speed()
	if m.overlay != nil {
		m.overlay.SetMouseScrollSpeed(m.mouseScrollSpeed)
	}
}

// MouseScrollSpeed reports the live transcript wheel multiplier (Rust #50209).
func (m *Model) MouseScrollSpeed() float64 {
	if m == nil {
		return codextui.MouseScrollSpeedDefault
	}
	return m.mouseScroll.Speed()
}

// TranscriptYOffset reports the live transcript's scroll offset from the top of
// the buffered output. This is the surface the wheel moves (Rust #50209), exposed
// so the host's configuration wiring can assert the configured speed end to end.
func (m *Model) TranscriptYOffset() int {
	if m == nil {
		return 0
	}
	return m.transcript.YOffset
}

// seedOverlayMouseScrollSpeed binds a newly created transcript overlay to the
// live multiplier (Rust #50209 passes it to `TranscriptOverlay::new`).
func (m *Model) seedOverlayMouseScrollSpeed() {
	if m == nil || m.overlay == nil {
		return
	}
	m.overlay.SetMouseScrollSpeed(m.mouseScrollSpeed)
}
