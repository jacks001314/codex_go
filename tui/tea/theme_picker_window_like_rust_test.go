package tea

import (
	"strings"
	"testing"

	bubbletea "github.com/charmbracelet/bubbletea"

	codextui "codex_go/tui"
	"codex_go/utils"
)

func TestThemePickerNarrowViewportDropsPreviewAndKeepsControlsLikeRust(t *testing.T) {
	model := NewModel(codextui.NewState(nil), Options{Width: 50, Height: 18})
	model.openThemePicker()
	view := utils.StripANSI(model.View())
	if rows := strings.Count(view, "\n") + 1; rows > model.height {
		t.Fatalf("theme picker rendered %d rows into %d:\n%s", rows, model.height, view)
	}
	for _, want := range []string{"Select Syntax Theme", "Type to filter themes...", "enter select · esc back"} {
		if !strings.Contains(view, want) {
			t.Fatalf("theme picker missing %q:\n%s", want, view)
		}
	}
	if !strings.Contains(view, "↓") {
		t.Fatalf("theme picker missing the below-overflow marker:\n%s", view)
	}
	if strings.Contains(view, "fn greet") {
		t.Fatalf("narrow picker kept the preview instead of yielding it:\n%s", view)
	}
	visible := model.modal.themeVisibleRows
	if visible <= 0 || visible >= len(model.modal.themePicker.Themes) {
		t.Fatalf("theme visible rows = %d", visible)
	}
	before := model.modal.themePicker.Selected
	model.Update(key(bubbletea.KeyPgDown))
	if model.modal.themePicker.Selected == before {
		t.Fatal("page down did not move the theme selection")
	}
	view = utils.StripANSI(model.View())
	if !strings.Contains(view, "↑") || !strings.Contains(view, "Select Syntax Theme") ||
		!strings.Contains(view, "enter select · esc back") {
		t.Fatalf("page down lost controls or the above-overflow marker:\n%s", view)
	}
	model.Update(key(bubbletea.KeyEnd))
	view = utils.StripANSI(model.View())
	if !strings.Contains(view, "Select Syntax Theme") || !strings.Contains(view, "enter select · esc back") {
		t.Fatalf("end lost picker controls:\n%s", view)
	}
}

func TestThemePickerWidePreviewSurvivesWindowLikeRust(t *testing.T) {
	model := NewModel(codextui.NewState(nil), Options{Width: 120, Height: 24})
	model.openThemePicker()
	view := utils.StripANSI(model.View())
	if rows := strings.Count(view, "\n") + 1; rows > model.height {
		t.Fatalf("wide theme picker rendered %d rows into %d", rows, model.height)
	}
	if !strings.Contains(view, "fn summarize") {
		t.Fatalf("wide picker dropped its side preview:\n%s", view)
	}
	if !strings.Contains(view, "Select Syntax Theme") || !strings.Contains(view, "enter select · esc back") {
		t.Fatalf("wide picker missing controls:\n%s", view)
	}
}
