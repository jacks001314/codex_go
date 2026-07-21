// Package overlay provides a stack-based dialog/overlay system for the TUI.
// It replaces the previous single-slot modalState pattern with a proper
// push/pop stack that supports any number of Dialog implementations.
package overlay

import (
	bubbletea "github.com/charmbracelet/bubbletea"
)

// =============================================================================
// Dialog interface
// =============================================================================

// Dialog is a full-screen or partial overlay that takes over user input.
// Each dialog manages its own state, key handling, and rendering.
type Dialog interface {
	// ID returns a unique identifier for this dialog instance.
	ID() string

	// Update handles a Bubble Tea message and returns the resulting action.
	Update(msg bubbletea.Msg) DialogResult

	// View renders the dialog content for the given terminal dimensions.
	View(width, height int) string
}

// =============================================================================
// DialogResult — typed action returned by Dialog.Update()
// =============================================================================

// DialogAction describes the lifecycle action a dialog wants to perform.
type DialogAction int

const (
	// ActionNone means the dialog handled the message but has no lifecycle change.
	ActionNone DialogAction = iota

	// ActionClose closes this dialog (pops it from the stack) without a result.
	ActionClose

	// ActionSubmit closes this dialog and returns a value.
	ActionSubmit

	// ActionCancel closes this dialog, indicating the user cancelled.
	ActionCancel

	// ActionRepaint signals that the dialog's visual state changed and a
	// re-render is needed, but the dialog should remain on the stack.
	ActionRepaint
)

// DialogResult is the return value of Dialog.Update().
type DialogResult struct {
	Action DialogAction
	Cmd    bubbletea.Cmd
	Value  any // optional result value (meaning depends on dialog)
}

// =============================================================================
// Overlay stack
// =============================================================================

// Overlay manages a stack of Dialog instances. Only the topmost dialog
// receives Update messages and its View is rendered.
//
// During initial migration from the single-slot modalState, the stack
// enforces a maximum depth of 1 to maintain backward compatibility.
// This restriction will be lifted once all dialogs are migrated.
type Overlay struct {
	stack   []Dialog
	maxSize int // 0 = unlimited, 1 during migration
}

// NewOverlay creates a new overlay stack. During migration mode
// (migrate=true), the stack enforces max depth of 1.
func NewOverlay(migrate bool) *Overlay {
	maxSize := 0 // unlimited
	if migrate {
		maxSize = 1
	}
	return &Overlay{maxSize: maxSize}
}

// Push adds a dialog to the top of the stack.
// During migration mode, if a dialog is already active, the old one
// is popped first (maintaining single-dialog behavior).
func (o *Overlay) Push(d Dialog) {
	if o.maxSize == 1 && len(o.stack) > 0 {
		o.stack = o.stack[:len(o.stack)-1]
	}
	o.stack = append(o.stack, d)
}

// Pop removes and returns the topmost dialog. Returns nil if the stack is empty.
func (o *Overlay) Pop() Dialog {
	if len(o.stack) == 0 {
		return nil
	}
	top := o.stack[len(o.stack)-1]
	o.stack = o.stack[:len(o.stack)-1]
	return top
}

// Top returns the topmost dialog without removing it. Returns nil if empty.
func (o *Overlay) Top() Dialog {
	if len(o.stack) == 0 {
		return nil
	}
	return o.stack[len(o.stack)-1]
}

// Active reports whether any dialog is currently on the stack.
func (o *Overlay) Active() bool {
	return len(o.stack) > 0
}

// Depth returns the number of dialogs on the stack.
func (o *Overlay) Depth() int {
	return len(o.stack)
}

// Update routes a message to the topmost dialog and processes lifecycle actions.
// Returns a Bubble Tea command to execute.
func (o *Overlay) Update(msg bubbletea.Msg) bubbletea.Cmd {
	if !o.Active() {
		return nil
	}
	result := o.Top().Update(msg)
	switch result.Action {
	case ActionClose, ActionSubmit, ActionCancel:
		o.Pop()
	case ActionNone, ActionRepaint:
		// dialog stays on stack
	}
	return result.Cmd
}

// View renders the topmost dialog. Returns empty string if no dialog is active.
func (o *Overlay) View(width, height int) string {
	if !o.Active() {
		return ""
	}
	return o.Top().View(width, height)
}

// Clear removes all dialogs from the stack.
func (o *Overlay) Clear() {
	o.stack = nil
}
