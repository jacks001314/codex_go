package tea

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// modalCenteredOverlayKind limits the retained-backdrop presentation to the
// confirmation dialogs Rust builds through `SelectionViewParams::confirmation`
// (ViewPresentation::Centered, Rust #50504). Ordinary pickers such as `/model`,
// `/theme`, and the Windows sandbox prompt use `picker()` and stay inline.
func modalCenteredOverlayKind(kind ModalKind) bool {
	switch kind {
	case ModalKindGeneric,
		ModalKindReview,
		ModalKindRunningTaskExit,
		ModalKindMisalignment,
		ModalKindRateLimitSwitch:
		return true
	default:
		return false
	}
}

// centeredDialogWidth mirrors Rust's view_stack::dialog_area width:
// `area.width - 4`, floored at `min(area.width, 8)`, capped at 72.
func centeredDialogWidth(width int) int {
	if width <= 0 {
		return 1
	}
	dialog := width - 4
	if minimum := min(width, 8); dialog < minimum {
		dialog = minimum
	}
	return min(dialog, 72)
}

func (m *Model) renderCenteredModalOverlay() string {
	if m == nil || m.modal == nil {
		return ""
	}
	// Rust renders the view inside the dialog rect, so its rows fill the
	// dialog width rather than the full terminal width.
	dialogWidth := centeredDialogWidth(m.width)
	savedWidth := m.width
	m.width = dialogWidth
	dialog := m.renderModal()
	m.width = savedWidth
	if strings.TrimSpace(dialog) == "" {
		return ""
	}
	// The backdrop is the previous view in the stack: a parent picker or the
	// composer when no other view is open.
	savedModal := m.modal
	parents := m.modalParents
	if len(parents) > 0 {
		m.modal = parents[len(parents)-1]
		m.modalParents = parents[:len(parents)-1]
	} else {
		m.modal = nil
	}
	base := m.View()
	m.modal = savedModal
	m.modalParents = parents
	return overlayCenteredDialog(base, dialog, m.width, m.height)
}

// overlayCenteredDialog clears the centered dialog rect and paints the view
// there, preserving the surrounding backdrop cells (Rust view_stack::CenteredView).
func overlayCenteredDialog(base, dialog string, width, height int) string {
	if width <= 0 || height <= 0 || strings.TrimSpace(dialog) == "" {
		return base
	}
	dialogWidth := centeredDialogWidth(width)
	wrapped := []string{}
	for _, line := range strings.Split(dialog, "\n") {
		if ansi.StringWidth(line) <= dialogWidth {
			wrapped = append(wrapped, line)
			continue
		}
		wrapped = append(wrapped, strings.Split(ansi.Wrap(line, dialogWidth, ""), "\n")...)
	}
	if len(wrapped) > height {
		wrapped = wrapped[:height]
	}
	dialogHeight := len(wrapped)
	x := (width - dialogWidth) / 2
	y := (height - dialogHeight) / 2

	baseLines := strings.Split(base, "\n")
	for len(baseLines) < height {
		baseLines = append(baseLines, "")
	}
	if len(baseLines) > height {
		baseLines = baseLines[:height]
	}
	for index := 0; index < dialogHeight; index++ {
		row := y + index
		if row < 0 || row >= len(baseLines) {
			continue
		}
		line := padANSIWidth(wrapped[index], dialogWidth)
		padded := padANSIWidth(baseLines[row], width)
		prefix := ansi.Cut(padded, 0, x)
		suffix := ansi.Cut(padded, x+dialogWidth, width)
		baseLines[row] = prefix + line + suffix
	}
	return strings.Join(baseLines, "\n")
}

func padANSIWidth(line string, width int) string {
	if current := ansi.StringWidth(line); current < width {
		return line + strings.Repeat(" ", width-current)
	}
	return line
}
