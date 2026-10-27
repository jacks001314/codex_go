package tea

import bubbletea "github.com/charmbracelet/bubbletea"

// Rust parity: codex-rs/tui/src/tui.rs `MouseCapture::{Inherit, Enabled,
// Disabled}` and `MouseCapture::captures_mouse` (#57284).
//
// Go emits Bubble Tea mouse commands instead of driving crossterm directly, but
// the policy precedence is the same: an inherited policy follows screen
// ownership, transcript/usage overlays always capture, and static pagers hand
// gestures back to the terminal.
type mouseCapturePolicy int

const (
	// mouseCaptureInherit captures only when the TUI owns the screen.
	mouseCaptureInherit mouseCapturePolicy = iota
	// mouseCaptureEnabled always captures mouse events.
	mouseCaptureEnabled
	// mouseCaptureDisabled leaves gestures (selection, links) to the terminal.
	mouseCaptureDisabled
)

// captures reports whether the policy requests terminal mouse reporting for the
// given screen ownership, mirroring Rust `MouseCapture::captures_mouse`.
func (p mouseCapturePolicy) captures(ownedScreen bool) bool {
	switch p {
	case mouseCaptureEnabled:
		return true
	case mouseCaptureDisabled:
		return false
	default:
		return ownedScreen
	}
}

// desiredMouseCapture resolves the active surface's policy. Overlays own the
// decision while open; otherwise expanded async questions hand link gestures
// back to the terminal unless an interactive modal is up (Rust #57285).
func (m *Model) desiredMouseCapture() mouseCapturePolicy {
	if m == nil {
		return mouseCaptureInherit
	}
	if m.overlay != nil {
		return m.overlayMouseCapture
	}
	if m.ownedScreen && m.modal == nil && m.asyncQuestions.Expanded() {
		return mouseCaptureDisabled
	}
	return mouseCaptureInherit
}

// syncMouseCapture emits a terminal command when the capture state changes. The
// state is retained across temporary editor, suspend, and panic handoffs so a
// close path can always restore the inherited policy.
func (m *Model) syncMouseCapture() bubbletea.Cmd {
	if m == nil {
		return nil
	}
	policy := m.desiredMouseCapture()
	m.mouseCapturePolicy = policy
	desired := policy.captures(m.ownedScreen)
	if desired == m.mouseCaptureActive {
		return nil
	}
	m.mouseCaptureActive = desired
	if desired {
		return bubbletea.EnableMouseCellMotion
	}
	return bubbletea.DisableMouse
}
