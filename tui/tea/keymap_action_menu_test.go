package tea

import (
	"strings"
	"testing"

	codextui "codex_go/tui"
	"codex_go/tui/chatwidget"
)

// Mirrors Rust's keymap_action_menu_responsive snapshot: the action menu hides
// its description column at 48 columns of row width and keeps an aligned column
// at 96 (#46691). The modal reserves four columns of horizontal inset, so the
// terminal widths below are the row widths plus four.
func TestModelKeymapActionMenuResponsiveWidthsMatchRust(t *testing.T) {
	custom := false
	view := chatwidget.NewKeymapActionMenuView(chatwidget.KeymapActionItem{
		Action:           "open_transcript",
		Bindings:         []string{"ctrl-t"},
		HasCustomBinding: &custom,
	})
	for _, rowWidth := range []int{48, 96} {
		terminalWidth := rowWidth + 4
		model := NewModel(nil, Options{Width: terminalWidth, Height: 24})
		model.openSelectionViewModal(ModalKindGeneric, view)
		rendered := ansiSequenceRE.ReplaceAllString(model.renderModal(), "")
		for _, line := range strings.Split(rendered, "\n") {
			if got := codextui.DisplayWidth(line); got > terminalWidth {
				t.Fatalf("width %d line %q is %d columns", terminalWidth, line, got)
			}
		}
		if !strings.Contains(rendered, "\u2013  Remove custom binding (disabled)") || strings.Contains(rendered, "3. Remove custom binding") {
			t.Fatalf("row width %d disabled gutter mismatch:\n%s", rowWidth, rendered)
		}
		if rowWidth == 48 {
			if strings.Contains(rendered, "Capture a replacement key") {
				t.Fatalf("row width 48 should hide the narrow description column:\n%s", rendered)
			}
			if !strings.Contains(rendered, "\u203a 1. Replace binding") || !strings.Contains(rendered, "  3. Back to shortcuts") {
				t.Fatalf("row width 48 option rows mismatch:\n%s", rendered)
			}
			continue
		}
		var twoColumn bool
		for _, line := range strings.Split(rendered, "\n") {
			if strings.Contains(line, "Replace binding") && strings.Contains(line, "Capture a replacement key for `ctrl-t`.") {
				twoColumn = true
			}
		}
		if !twoColumn {
			t.Fatalf("row width 96 should keep the description in its column:\n%s", rendered)
		}
	}
}
