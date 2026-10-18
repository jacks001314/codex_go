package tea

import (
	"strings"
	"testing"

	codextui "codex_go/tui"
	chatwidget "codex_go/tui/chatwidget"
)

func TestSelectionViewsUseCompactPickerHintLikeRust(t *testing.T) {
	model := NewModel(codextui.NewState(nil), Options{Width: 80, Height: 24})
	model.openSelectionViewModal(ModalKindGeneric, chatwidget.SelectionView{
		ViewID:      "picker-hint",
		Title:       "Choose",
		AllowCancel: true,
		FooterHint:  chatwidget.StandardPopupHintLine,
		Items:       []chatwidget.SelectionItem{{ID: "one", Name: "One"}},
	})
	if model.modal == nil || model.modal.footerHint != chatwidget.PickerPopupHintLine {
		t.Fatalf("footer hint = %#v", model.modal)
	}
	if view := model.View(); !strings.Contains(view, "enter select · esc back") {
		t.Fatalf("compact picker hint missing:\n%s", view)
	}

	model.openSelectionViewModal(ModalKindGeneric, chatwidget.SelectionView{
		ViewID:      "custom-hint",
		Title:       "Choose",
		AllowCancel: true,
		FooterHint:  "left/right group · enter edit · esc close",
		Items:       []chatwidget.SelectionItem{{ID: "one", Name: "One"}},
	})
	if model.modal == nil || model.modal.footerHint != "left/right group · enter edit · esc close" {
		t.Fatalf("custom footer hint was rewritten: %#v", model.modal)
	}

	status := chatwidget.NewStatusLineSetupView(nil, false, chatwidget.StatusSurfacePreviewData{})
	if status.FooterHint != "space toggle · enter save · esc cancel" {
		t.Fatalf("status setup footer = %q", status.FooterHint)
	}
	title := chatwidget.NewTerminalTitleSetupView(nil, chatwidget.StatusSurfacePreviewData{})
	if title.FooterHint != "space toggle · enter save · esc cancel" {
		t.Fatalf("terminal title setup footer = %q", title.FooterHint)
	}
	model.featureSettings = map[string]bool{"memories": true}
	model.openMemoriesSettings()
	if model.modal == nil || model.modal.footerHint != "space toggle · enter save/select · esc cancel" {
		t.Fatalf("memories footer hint = %#v", model.modal)
	}
	if view := model.View(); !strings.Contains(view, "space toggle · enter save/select · esc cancel") {
		t.Fatalf("memories compact picker hint missing:\n%s", view)
	}
}
