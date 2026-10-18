package tea

import (
	"strings"
	"testing"

	codextui "codex_go/tui"
	"codex_go/tui/chatwidget"
	"codex_go/utils"
)

// Rust #50504: a generic confirmation renders as a centered dialog over the
// retained composer instead of replacing the bottom pane.
func TestGenericConfirmationRetainsComposerBackdropLikeRust(t *testing.T) {
	model := NewModel(codextui.NewState(nil), Options{
		Width:       80,
		Height:      24,
		Placeholder: "COMPOSER-MARKER",
	})
	model.openSelectionViewModal(ModalKindGeneric, chatwidget.SelectionView{
		ViewID:   "confirm-delete",
		Title:    "Confirm delete",
		Subtitle: "This action cannot be undone.",
		Items: []chatwidget.SelectionItem{
			{ID: "delete", Name: "Delete"},
			{ID: "cancel", Name: "Cancel"},
		},
	})
	view := utils.StripANSI(model.View())
	if !strings.Contains(view, "COMPOSER-MARKER") {
		t.Fatalf("confirmation removed the composer backdrop:\n%s", view)
	}
	if !strings.Contains(view, "Confirm delete") {
		t.Fatalf("confirmation dialog missing:\n%s", view)
	}
	titleLine := ""
	for _, line := range strings.Split(view, "\n") {
		if strings.Contains(line, "Confirm delete") {
			titleLine = line
			break
		}
	}
	if titleLine == "" || len(titleLine)-len(strings.TrimLeft(titleLine, " ")) == 0 {
		t.Fatalf("confirmation was not centered: %q", titleLine)
	}
	if lines := strings.Count(strings.TrimRight(view, "\n"), "\n") + 1; lines > 24 {
		t.Fatalf("overlay exceeded terminal height: %d", lines)
	}
}
