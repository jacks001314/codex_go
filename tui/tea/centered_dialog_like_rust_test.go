package tea

import (
	"strings"
	"testing"

	bubbletea "github.com/charmbracelet/bubbletea"

	codextui "codex_go/tui"
	"codex_go/tui/chatwidget"
	"codex_go/utils"
)

// Rust #50504: a generic confirmation renders as a centered dialog over the
// retained composer instead of replacing the bottom pane.
func TestGenericConfirmationRetainsComposerBackdropLikeRust(t *testing.T) {
	for _, kind := range []ModalKind{
		ModalKindGeneric,
		ModalKindReview,
		ModalKindRunningTaskExit,
		ModalKindMisalignment,
		ModalKindRateLimitSwitch,
	} {
		t.Run(string(kind), func(t *testing.T) {
			model := NewModel(codextui.NewState(nil), Options{
				Width:       80,
				Height:      24,
				Placeholder: "COMPOSER-MARKER",
			})
			model.openSelectionViewModal(kind, chatwidget.SelectionView{
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
		})
	}
}

func TestNestedConfirmationRetainsParentPickerLikeRust(t *testing.T) {
	model := NewModel(codextui.NewState(nil), Options{
		Width:       90,
		Height:      28,
		Placeholder: "COMPOSER-MARKER",
	})
	model.openSelectionViewModal(ModalKindPicker, chatwidget.SelectionView{
		ViewID: "parent-picker",
		Title:  "Parent Picker",
		Items:  []chatwidget.SelectionItem{{ID: "one", Name: "One"}},
	})
	model.openSelectionViewModal(ModalKindGeneric, chatwidget.SelectionView{
		ViewID: "child-confirm",
		Title:  "Child Confirm",
		Items: []chatwidget.SelectionItem{
			{ID: "yes", Name: "Yes"},
			{ID: "no", Name: "No"},
		},
	})
	if model.modal == nil || model.modal.title != "Child Confirm" || len(model.modalParents) != 1 {
		t.Fatalf("modal stack = child %#v parents=%d", model.modal, len(model.modalParents))
	}
	view := utils.StripANSI(model.View())
	if !strings.Contains(view, "Child Confirm") || !strings.Contains(view, "COMPOSER-MARKER") {
		t.Fatalf("nested overlay missing child/backdrop:\n%s", view)
	}
	model.Update(bubbletea.KeyMsg{Type: bubbletea.KeyEsc})
	if model.modal == nil || model.modal.title != "Parent Picker" || len(model.modalParents) != 0 {
		t.Fatalf("parent was not restored: modal=%#v parents=%d", model.modal, len(model.modalParents))
	}
	if view := utils.StripANSI(model.View()); strings.Contains(view, "Child Confirm") || !strings.Contains(view, "Parent Picker") {
		t.Fatalf("child dialog did not restore parent:\n%s", view)
	}
}
