package tea

import (
	"fmt"
	"strings"
	"testing"

	bubbletea "github.com/charmbracelet/bubbletea"

	codextui "codex_go/tui"
	bottompane "codex_go/tui/bottom_pane"
	chatwidget "codex_go/tui/chatwidget"
	"codex_go/utils"
)

func modalPickerItems(count int) []chatwidget.SelectionItem {
	items := make([]chatwidget.SelectionItem, 0, count)
	for index := 0; index < count; index++ {
		items = append(items, chatwidget.SelectionItem{
			ID:          fmt.Sprintf("item-%02d", index),
			Name:        fmt.Sprintf("Item %02d", index),
			Description: fmt.Sprintf("Description %02d", index),
		})
	}
	return items
}

func TestModalPickerWindowsLongListsLikeRust(t *testing.T) {
	model := NewModel(codextui.NewState(nil), Options{Width: 80, Height: 18})
	model.openSelectionViewModal(ModalKindGeneric, chatwidget.SelectionView{
		ViewID:      "long-picker",
		Title:       "Pick an item",
		Subtitle:    "A long list keeps its controls visible.",
		AllowCancel: true,
		FooterHint:  chatwidget.PickerPopupHintLine,
		Items:       modalPickerItems(40),
	})
	view := utils.StripANSI(model.View())
	if rows := strings.Count(view, "\n") + 1; rows > model.height {
		t.Fatalf("picker rendered %d rows into a %d-row terminal", rows, model.height)
	}
	for _, want := range []string{"Pick an item", "Item 00", "enter select · esc back", "↓"} {
		if !strings.Contains(view, want) {
			t.Fatalf("picker missing %q:\n%s", want, view)
		}
	}
	if model.modal.optionVisibleRows <= 0 || model.modal.optionVisibleRows >= 40 {
		t.Fatalf("visible rows = %d", model.modal.optionVisibleRows)
	}

	page := model.modal.optionVisibleRows
	model.Update(key(bubbletea.KeyPgDown))
	if model.modal.selected != page {
		t.Fatalf("page down selected %d, want %d", model.modal.selected, page)
	}
	view = utils.StripANSI(model.View())
	if !strings.Contains(view, fmt.Sprintf("Item %02d", page)) || !strings.Contains(view, "↑") {
		t.Fatalf("page down lost the selected row or overflow marker:\n%s", view)
	}

	model.Update(key(bubbletea.KeyEnd))
	if model.modal.selected != 39 {
		t.Fatalf("end selected %d", model.modal.selected)
	}
	view = utils.StripANSI(model.View())
	if !strings.Contains(view, "Item 39") || !strings.Contains(view, "Pick an item") ||
		!strings.Contains(view, "enter select · esc back") {
		t.Fatalf("end lost controls or the last row:\n%s", view)
	}

	model.Update(key(bubbletea.KeyHome))
	if model.modal.selected != 0 {
		t.Fatalf("home selected %d", model.modal.selected)
	}
}

func TestModalPickerShortListKeepsAllRowsLikeRust(t *testing.T) {
	model := NewModel(codextui.NewState(nil), Options{Width: 80, Height: 24})
	model.openSelectionViewModal(ModalKindGeneric, chatwidget.SelectionView{
		ViewID:      "short-picker",
		Title:       "Pick",
		AllowCancel: true,
		FooterHint:  chatwidget.PickerPopupHintLine,
		Items:       modalPickerItems(3),
	})
	view := utils.StripANSI(model.View())
	for _, want := range []string{"Item 00", "Item 01", "Item 02"} {
		if !strings.Contains(view, want) {
			t.Fatalf("short picker missing %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "↑") || strings.Contains(view, "↓") {
		t.Fatalf("short picker rendered overflow markers:\n%s", view)
	}
}

func TestResponsiveModalPickerUsesVisibleRowsLikeRust(t *testing.T) {
	model := NewModel(codextui.NewState(nil), Options{Width: 60, Height: 20})
	model.openSelectionViewModal(ModalKindGeneric, chatwidget.SelectionView{
		ViewID:            "responsive-long-picker",
		Title:             "Pick",
		AllowCancel:       true,
		FooterHint:        chatwidget.PickerPopupHintLine,
		Items:             modalPickerItems(30),
		DescriptionLayout: bottompane.NewHideWhenNarrowDescriptionLayout(10),
	})
	view := utils.StripANSI(model.View())
	if rows := strings.Count(view, "\n") + 1; rows > model.height {
		t.Fatalf("responsive picker rendered %d rows into %d", rows, model.height)
	}
	if model.modal.optionVisibleRows <= 0 || model.modal.optionVisibleRows >= 30 {
		t.Fatalf("responsive visible rows = %d", model.modal.optionVisibleRows)
	}
	if !strings.Contains(view, "Item 00") || !strings.Contains(view, "↓") {
		t.Fatalf("responsive picker missing first row or overflow marker:\n%s", view)
	}
}

func TestResponsiveModalPickerCountsWrappedRowsLikeRust(t *testing.T) {
	items := modalPickerItems(20)
	for index := range items {
		items[index].Description = strings.Repeat("wrapped description ", 6)
	}
	model := NewModel(codextui.NewState(nil), Options{Width: 80, Height: 18})
	model.openSelectionViewModal(ModalKindGeneric, chatwidget.SelectionView{
		ViewID:            "wrapped-picker",
		Title:             "Pick",
		AllowCancel:       true,
		FooterHint:        chatwidget.PickerPopupHintLine,
		Items:             items,
		DescriptionLayout: bottompane.NewHideWhenNarrowDescriptionLayout(1),
	})
	view := utils.StripANSI(model.View())
	if rows := strings.Count(view, "\n") + 1; rows > model.height {
		t.Fatalf("wrapped picker rendered %d rows into %d", rows, model.height)
	}
	if model.modal.optionVisibleRows <= 0 || model.modal.optionVisibleRows >= 20 {
		t.Fatalf("wrapped visible rows = %d", model.modal.optionVisibleRows)
	}
	if !strings.Contains(view, "Item 00") || !strings.Contains(view, "↓") {
		t.Fatalf("wrapped picker missing first row or overflow marker:\n%s", view)
	}
}
