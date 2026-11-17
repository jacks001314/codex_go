package tea

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	codextui "codex_go/tui"
	"codex_go/tui/mousemsg"
)

// Rust app/owned_transcript.rs and sidebar_chooser.rs: the row under the
// pointer paints the stronger hover fill, the selected row keeps the selection
// fill, and the full-path view or a pointer outside the pane has no hover row.
func TestSidebarChooserRowHoverLikeRust(t *testing.T) {
	restore := codextui.SetStdoutColorLevelForTest(codextui.ColorANSI256)
	defer restore()

	model, _ := newSidebarTabsModel(t)
	openChooserWithFiles(t, model, nil, "")
	model.sidebarChoices = []SideChatChoice{{ThreadID: "saved-1"}, {ThreadID: "saved-2"}}
	model.transcript.Height = 24
	model.sidebarTabStripTop = 0
	width := model.sidebarTabStripWidth()
	rows := strings.Split(model.renderSidebarChooser(width, 24), "\n")

	rowForChoice := func(want int) int {
		t.Helper()
		for row := 0; row < len(rows); row++ {
			if index, ok := model.sidebarChooserChoiceAtRow(row); ok && index == want {
				return row
			}
		}
		t.Fatalf("no chooser row maps to choice %d", want)
		return -1
	}
	hoverRow := rowForChoice(1)
	if strings.Contains(rows[hoverRow], "\x1b[") {
		t.Fatalf("an unhovered row painted a style: %q", rows[hoverRow])
	}

	left, _, _, ok := model.sidePaneLayout(model.width)
	if !ok {
		t.Fatal("side pane layout unavailable")
	}
	insideX := left + ansi.StringWidth(sideWorkspaceDivider) + 2
	_, _ = model.handleSidePaneMouse(mousemsg.MouseMsg{
		Action: mousemsg.MouseActionMotion,
		X:      insideX,
		Y:      hoverRow,
	})
	if model.sidebarChooserHover != 1 {
		t.Fatalf("hover index = %d, want 1", model.sidebarChooserHover)
	}
	hovered := strings.Split(model.renderSidebarChooser(width, 24), "\n")[hoverRow]
	if !strings.Contains(hovered, "\x1b[") {
		t.Fatalf("hovered row painted no style: %q", hovered)
	}
	if strings.TrimRight(ansi.Strip(hovered), " ") != strings.TrimRight(ansi.Strip(rows[hoverRow]), " ") {
		t.Fatalf("hover changed the row text: %q vs %q", ansi.Strip(hovered), ansi.Strip(rows[hoverRow]))
	}

	// The pointer leaving the pane clears the hover.
	if _, _ = model.handleSidePaneMouse(mousemsg.MouseMsg{
		Action: mousemsg.MouseActionMotion,
		X:      0,
		Y:      hoverRow,
	}); model.sidebarChooserHover != -1 {
		t.Fatalf("hover after leaving the pane = %d, want -1", model.sidebarChooserHover)
	}

	// The full-path view has no row targets (Rust checks `full_path.is_none()`).
	model.sidebarShowingFilePath = true
	if _, _ = model.handleSidePaneMouse(mousemsg.MouseMsg{
		Action: mousemsg.MouseActionMotion,
		X:      insideX,
		Y:      hoverRow,
	}); model.sidebarChooserHover != -1 {
		t.Fatalf("hover in the full-path view = %d, want -1", model.sidebarChooserHover)
	}
	model.sidebarShowingFilePath = false

	// The selected row carries the selection fill even without a pointer.
	model.sidebarChoice = 1
	selected := strings.Split(model.renderSidebarChooser(width, 24), "\n")[rowForChoice(1)]
	if !strings.Contains(selected, "\x1b[") {
		t.Fatalf("selected row painted no style: %q", selected)
	}
}
