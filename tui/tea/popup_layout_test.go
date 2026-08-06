package tea

import (
	"strings"
	"testing"

	bubbletea "github.com/charmbracelet/bubbletea"

	codextui "codex_go/tui"
)

// TestPopupKeepsViewWithinTerminalHeight guards against the slash/skill popup
// overflowing the terminal. The animation engine repaints at 20 FPS, so a view
// taller than the terminal makes it auto-scroll continuously.
func TestPopupKeepsViewWithinTerminalHeight(t *testing.T) {
	for _, tc := range []struct{ w, h int }{
		{61, 32},
		{120, 30},
		{80, 24},
	} {
		model := NewModel(codextui.NewState(nil), Options{Width: tc.w, Height: tc.h})
		typeText(t, model, "/")
		view := model.View()
		lines := strings.Split(strings.TrimRight(view, "\n"), "\n")
		if len(lines) > tc.h {
			t.Fatalf("size %dx%d: slash popup view = %d lines, exceeds terminal height %d", tc.w, tc.h, len(lines), tc.h)
		}
		if !strings.Contains(view, codextui.SelectionPrefix(true)+"/model") {
			t.Fatalf("size %dx%d: slash popup missing from view:\n%s", tc.w, tc.h, view)
		}
		// Closing the popup must restore the full transcript height.
		model.Update(key(bubbletea.KeyEsc))
		if model.slashPopup.Active {
			t.Fatalf("size %dx%d: slash popup still active after Esc", tc.w, tc.h)
		}
		if model.View() == view {
			t.Fatalf("size %dx%d: view unchanged after closing popup", tc.w, tc.h)
		}
	}
}
