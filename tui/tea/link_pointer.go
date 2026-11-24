package tea

import (
	"io"

	"codex_go/shell"
)

// Rust #62390 (tui/src/tui/link_pointer.rs): a hovered clickable region also
// changes the OS pointer shape through OSC 22. Only Ghostty and Kitty honour
// the CSS pointer names, and a multiplexer may swallow the sequence, so the
// escape is emitted only for a bare Ghostty or Kitty terminal.
const (
	pointerShapeUntouched = iota
	pointerShapeDefault
	pointerShapeLink
)

// linkPointerState remembers the shape already written so redraws do not
// repeat the escape.
type linkPointerState struct {
	shape int
}

func pointerShapeEscape(shape int) string {
	switch shape {
	case pointerShapeLink:
		return "\x1b]22;pointer\x1b\\"
	case pointerShapeDefault:
		return "\x1b]22;default\x1b\\"
	default:
		return ""
	}
}

// update returns the escape that switches to the shape the hover state asks
// for, or an empty string when the terminal already shows it.
func (s *linkPointerState) update(hovering bool) string {
	if s == nil {
		return ""
	}
	next := pointerShapeDefault
	if hovering {
		next = pointerShapeLink
	}
	if s.shape == next || (s.shape == pointerShapeUntouched && next == pointerShapeDefault) {
		return ""
	}
	s.shape = next
	return pointerShapeEscape(next)
}

// restore releases the override when the TUI stops owning the pointer. Kitty
// keeps a named shape across mouse capture and screen changes, so an empty
// OSC 22 returns it to the terminal's default policy; Ghostty keeps its
// text-cursor fallback.
func (s *linkPointerState) restore(kitty bool) string {
	if s == nil || s.shape == pointerShapeUntouched {
		return ""
	}
	s.shape = pointerShapeUntouched
	if kitty {
		return "\x1b]22;\x1b\\"
	}
	return "\x1b]22;text\x1b\\"
}

// pointerTerminalInfo reports the terminal identity used for OSC 22 gating.
func (m *Model) pointerTerminalInfo() *shell.TerminalInfo {
	if m == nil {
		return nil
	}
	if m.linkPointerTerminal == nil {
		m.linkPointerTerminal = shell.CurrentTerminalInfo()
	}
	return m.linkPointerTerminal
}

// pointerShapeSupported mirrors Rust's set_pointer_shape guard: no multiplexer
// and a terminal known to honour OSC 22 pointer names.
func (m *Model) pointerShapeSupported() bool {
	info := m.pointerTerminalInfo()
	if info == nil || info.Multiplexer != nil {
		return false
	}
	return info.Name == shell.TerminalGhostty || info.Name == shell.TerminalKitty
}

// pointerShapeOutput returns the escape that must precede this frame when the
// shape changed since the previous draw.
func (m *Model) pointerShapeOutput() string {
	if m == nil || !m.pointerShapeSupported() {
		return ""
	}
	if !m.linkHoverApplies() {
		return m.linkPointer.restore(m.pointerTerminalInfo().Name == shell.TerminalKitty)
	}
	return m.linkPointer.update(m.hoverTargetActive)
}

// restorePointerShape releases the OSC 22 override on exit, so Kitty does not
// keep a pointer shape after the TUI hands the terminal back (Rust #62390
// LinkPointer::restore).
func (m *Model) restorePointerShape(output io.Writer) {
	if m == nil || output == nil || !m.pointerShapeSupported() {
		return
	}
	if sequence := m.linkPointer.restore(m.pointerTerminalInfo().Name == shell.TerminalKitty); sequence != "" {
		_, _ = io.WriteString(output, sequence)
	}
}
