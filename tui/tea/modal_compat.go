package tea

import (
	bubbletea "github.com/charmbracelet/bubbletea"

	"codex_go/tui/overlay"
)

// ModalCompat wraps the legacy modalState so it can be used as an overlay.Dialog.
// During migration, openModal pushes a ModalCompat onto the overlay stack
// instead of setting m.modal directly. The existing modalState fields and
// methods continue to work; ModalCompat delegates to them.
type ModalCompat struct {
	model *Model
	state *modalState
}

// Ensure ModalCompat implements overlay.Dialog.
var _ overlay.Dialog = (*ModalCompat)(nil)

func newModalCompat(m *Model, state *modalState) *ModalCompat {
	return &ModalCompat{model: m, state: state}
}

func (c *ModalCompat) ID() string {
	if c.state == nil {
		return ""
	}
	return c.state.id
}

func (c *ModalCompat) Update(msg bubbletea.Msg) overlay.DialogResult {
	if c.model == nil || c.state == nil {
		return overlay.DialogResult{Action: overlay.ActionClose}
	}

	// Route through the existing modal update logic
	switch msg := msg.(type) {
	case bubbletea.KeyMsg:
		if msg.Type == bubbletea.KeyEsc {
			c.model.respondModal(true)
			return overlay.DialogResult{Action: overlay.ActionCancel}
		}
		if msg.Type == bubbletea.KeyEnter {
			c.model.respondModal(false)
			return overlay.DialogResult{Action: overlay.ActionSubmit}
		}
		// For other keys, delegate to updateModal
		c.model.updateModal(msg)
		return overlay.DialogResult{Action: overlay.ActionRepaint}
	}
	return overlay.DialogResult{Action: overlay.ActionNone}
}

func (c *ModalCompat) View(width, height int) string {
	if c.model == nil {
		return ""
	}
	return c.model.renderModal()
}
