// Package mousemsg provides the TUI's internal mouse event model.
//
// It preserves the v1 MouseMsg shape the TUI handlers were written against
// while Bubble Tea v2 splits events into MouseClickMsg/MouseReleaseMsg/
// MouseWheelMsg/MouseMotionMsg. FromTea normalizes them once at the Model
// boundary; Original retains the v2 message for Bubbles viewports.
package mousemsg

import tea "charm.land/bubbletea/v2"

type MouseAction int

const (
	MouseActionPress MouseAction = iota
	MouseActionRelease
	MouseActionMotion
)

type MouseButton int

const (
	MouseButtonNone MouseButton = iota
	MouseButtonLeft
	MouseButtonMiddle
	MouseButtonRight
	MouseButtonWheelUp
	MouseButtonWheelDown
	MouseButtonWheelLeft
	MouseButtonWheelRight
	MouseButtonBackward
	MouseButtonForward
	MouseButton10
	MouseButton11
)

type MouseMsg struct {
	X        int
	Y        int
	Shift    bool
	Alt      bool
	Ctrl     bool
	Action   MouseAction
	Button   MouseButton
	Original tea.Msg
}

func FromTea(message tea.Msg) (MouseMsg, bool) {
	switch msg := message.(type) {
	case MouseMsg:
		return msg, true
	case tea.MouseClickMsg:
		return fromMouse(msg.Mouse(), MouseActionPress, message), true
	case tea.MouseReleaseMsg:
		return fromMouse(msg.Mouse(), MouseActionRelease, message), true
	case tea.MouseWheelMsg:
		return fromMouse(msg.Mouse(), MouseActionPress, message), true
	case tea.MouseMotionMsg:
		return fromMouse(msg.Mouse(), MouseActionMotion, message), true
	}
	return MouseMsg{}, false
}

func fromMouse(m tea.Mouse, action MouseAction, original tea.Msg) MouseMsg {
	return MouseMsg{
		X:        m.X,
		Y:        m.Y,
		Shift:    m.Mod.Contains(tea.ModShift),
		Alt:      m.Mod.Contains(tea.ModAlt),
		Ctrl:     m.Mod.Contains(tea.ModCtrl),
		Action:   action,
		Button:   button(m.Button),
		Original: original,
	}
}

func button(b tea.MouseButton) MouseButton {
	switch b {
	case tea.MouseLeft:
		return MouseButtonLeft
	case tea.MouseMiddle:
		return MouseButtonMiddle
	case tea.MouseRight:
		return MouseButtonRight
	case tea.MouseWheelUp:
		return MouseButtonWheelUp
	case tea.MouseWheelDown:
		return MouseButtonWheelDown
	case tea.MouseWheelLeft:
		return MouseButtonWheelLeft
	case tea.MouseWheelRight:
		return MouseButtonWheelRight
	case tea.MouseBackward:
		return MouseButtonBackward
	case tea.MouseForward:
		return MouseButtonForward
	case tea.MouseButton10:
		return MouseButton10
	case tea.MouseButton11:
		return MouseButton11
	default:
		return MouseButtonNone
	}
}

func OriginalMessage(message tea.Msg) tea.Msg {
	mouse, ok := message.(MouseMsg)
	if !ok || mouse.Original == nil {
		return message
	}
	return mouse.Original
}
