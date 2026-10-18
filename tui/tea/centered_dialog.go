package tea

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// modalCenteredOverlayKind limits the retained-backdrop presentation to the
// generic confirmation/selection dialogs migrated so far (Rust #50504).
func modalCenteredOverlayKind(kind ModalKind) bool {
	switch kind {
	case ModalKindGeneric,
		ModalKindReview,
		ModalKindRunningTaskExit,
		ModalKindMisalignment,
		ModalKindRateLimitSwitch,
		ModalKindPermissions,
		ModalKindExperimental,
		ModalKindStatusLine,
		ModalKindTitle,
		ModalKindPresets,
		ModalKindConfig,
		ModalKindTheme,
		ModalKindPets,
		ModalKindMemories,
		ModalKindAutoReview,
		ModalKindGoal,
		ModalKindSkills,
		ModalKindManageSkills,
		ModalKindHooksBrowser,
		ModalKindAgent,
		ModalKindAgents,
		ModalKindUsage,
		ModalKindWindowsSandbox,
		ModalKindWorktree,
		ModalKindExternalImport,
		ModalKindPicker:
		return true
	default:
		return false
	}
}

func (m *Model) renderCenteredModalOverlay() string {
	if m == nil || m.modal == nil {
		return ""
	}
	savedWidth := m.width
	m.width = max(m.width-2, 1)
	dialog := m.renderModal()
	m.width = savedWidth
	if strings.TrimSpace(dialog) == "" {
		return ""
	}
	saved := m.modal
	m.modal = nil
	base := m.View()
	m.modal = saved
	return overlayCenteredDialog(base, dialog, m.width, m.height)
}

func overlayCenteredDialog(base, dialog string, width, height int) string {
	if width <= 0 || height <= 0 || strings.TrimSpace(dialog) == "" {
		return base
	}
	maxDialogWidth := max(width-2, 1)
	contentWidth := max(maxDialogWidth-2, 1)
	wrapped := []string{}
	for _, line := range strings.Split(dialog, "\n") {
		if ansi.StringWidth(line) <= contentWidth {
			wrapped = append(wrapped, line)
			continue
		}
		wrapped = append(wrapped, strings.Split(ansi.Wrap(line, contentWidth, ""), "\n")...)
	}
	dialog = strings.Join(wrapped, "\n")
	boxed := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("8")).
		MaxWidth(maxDialogWidth).
		Render(dialog)
	dialogLines := strings.Split(boxed, "\n")
	dialogWidth := 0
	for _, line := range dialogLines {
		dialogWidth = max(dialogWidth, ansi.StringWidth(line))
	}
	dialogHeight := len(dialogLines)
	if dialogWidth > width || dialogHeight > height {
		return appendCenteredFallback(base, boxed)
	}
	x := (width - dialogWidth) / 2
	y := (height - dialogHeight) / 2

	baseLines := strings.Split(base, "\n")
	for len(baseLines) < height {
		baseLines = append(baseLines, "")
	}
	for index, line := range dialogLines {
		row := y + index
		if row < 0 || row >= len(baseLines) {
			continue
		}
		line = padANSIWidth(line, dialogWidth)
		prefix := ansi.Cut(padANSIWidth(baseLines[row], width), 0, x)
		suffix := ansi.Cut(padANSIWidth(baseLines[row], width), x+dialogWidth, width)
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

func appendCenteredFallback(base, dialog string) string {
	if strings.TrimSpace(base) == "" {
		return dialog
	}
	return strings.TrimRight(base, "\n") + "\n" + dialog
}
