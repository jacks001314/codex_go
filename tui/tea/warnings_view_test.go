package tea

import (
	"strings"
	"testing"

	bubbletea "github.com/charmbracelet/bubbletea"

	codextui "codex_go/tui"
	historycell "codex_go/tui/history_cell"
	"codex_go/utils"
)

// Mirrors the #48205/#48206 wiring: F2 opens the retained-warnings viewer, the
// list actions page through the diagnostics, the draft is untouched, and closing
// dismisses the pages that were drawn.
func TestWarningsViewerOpensNavigatesAndDismissesLikeRust(t *testing.T) {
	enabled := true
	model := NewModel(codextui.NewState(nil), Options{
		Width:                 100,
		Height:                30,
		ShowTooltips:          &enabled,
		StartupConfigWarnings: []string{"First warning", "Second warning"},
	})
	model.composer.InsertString("draft")

	model.Update(bubbletea.KeyMsg{Type: bubbletea.KeyF2})
	if model.warningsView == nil {
		t.Fatal("F2 did not open the warnings viewer")
	}
	view := utils.StripANSI(model.View())
	if !strings.Contains(view, "Warnings \u00b7 1 of 2") || !strings.Contains(view, "First warning") {
		t.Fatalf("viewer did not show the first warning:\n%s", view)
	}
	if !strings.Contains(view, "keep & next") || !strings.Contains(view, "dismiss & close") {
		t.Fatalf("viewer footer missing hints:\n%s", view)
	}

	model.Update(bubbletea.KeyMsg{Type: bubbletea.KeyRight})
	view = utils.StripANSI(model.View())
	if !strings.Contains(view, "Warnings \u00b7 2 of 2") || !strings.Contains(view, "Second warning") {
		t.Fatalf("viewer did not page to the second warning:\n%s", view)
	}

	// Esc closes and dismisses the drawn pages; the draft is untouched.
	model.Update(bubbletea.KeyMsg{Type: bubbletea.KeyEsc})
	if model.warningsView != nil {
		t.Fatal("Esc did not close the warnings viewer")
	}
	if got := strings.TrimSpace(model.composer.Value()); got != "draft" {
		t.Fatalf("composer draft = %q, want it untouched", got)
	}
	if got := model.warningDisplay.VisibleEntries([]historycell.WarningCell{model.startupWarnings}); len(got) != 0 {
		t.Fatalf("drawn warnings still visible after close: %#v", got)
	}
}

// Mirrors the keep behaviour: plain `k` keeps the current page, and the global
// copy key copies only the current diagnostic.
func TestWarningsViewerKeepAndCopyLikeRust(t *testing.T) {
	enabled := true
	model := NewModel(codextui.NewState(nil), Options{
		Width:                 100,
		Height:                30,
		ShowTooltips:          &enabled,
		StartupConfigWarnings: []string{"First warning", "Second warning"},
	})
	var copied []string
	model.clipboardWrite = func(text string) error {
		copied = append(copied, text)
		return nil
	}
	model.Update(bubbletea.KeyMsg{Type: bubbletea.KeyF2})
	model.Update(bubbletea.KeyMsg{Type: bubbletea.KeyRunes, Runes: []rune{'k'}})
	if got := model.warningsView.CurrentIndex(); got != 1 {
		t.Fatalf("keep did not advance the viewer: index %d", got)
	}
	model.Update(bubbletea.KeyMsg{Type: bubbletea.KeyCtrlO})
	if len(copied) != 1 || copied[0] != "Second warning" {
		t.Fatalf("copy = %#v, want the current diagnostic", copied)
	}
	if model.warningsFlash != "Copied" {
		t.Fatalf("copy flash = %q, want Copied", model.warningsFlash)
	}
	// The kept first page survives the close; the unvisited page is neither
	// dismissed nor kept.
	model.Update(bubbletea.KeyMsg{Type: bubbletea.KeyEsc})
	visible := model.warningDisplay.VisibleEntries([]historycell.WarningCell{model.startupWarnings})
	if len(visible) != 2 {
		t.Fatalf("visible warnings after keep = %#v, want both", visible)
	}
}
