package tea

import (
	"testing"

	bubbletea "github.com/charmbracelet/bubbletea"
)

// Rust #59527: dragging the divider previews a width and saves the preference
// on release.
func TestSidePaneDividerDragSavesWidthLikeRust(t *testing.T) {
	m, edits := sidePaneResizeModel(t, 140, false)
	left, right, _, ok := m.sidePaneLayout(m.width)
	if !ok {
		t.Fatal("split layout unavailable")
	}
	handled, _ := m.handleSidePaneMouse(bubbletea.MouseMsg{
		X: left, Y: 3, Action: bubbletea.MouseActionPress, Button: bubbletea.MouseButtonLeft,
	})
	if !handled || m.sideResize == nil || m.sideResize.previewWidth != right {
		t.Fatalf("press handled=%v preview=%#v want %d", handled, m.sideResize, right)
	}
	handled, _ = m.handleSidePaneMouse(bubbletea.MouseMsg{
		X: left - 10, Y: 3, Action: bubbletea.MouseActionMotion, Button: bubbletea.MouseButtonLeft,
	})
	want := sidebarColumns(right+10, m.width)
	if !handled || m.sideResize == nil || m.sideResize.previewWidth != want {
		t.Fatalf("motion handled=%v preview=%#v want %d", handled, m.sideResize, want)
	}
	handled, saveCmd := m.handleSidePaneMouse(bubbletea.MouseMsg{
		X: left - 10, Y: 3, Action: bubbletea.MouseActionRelease, Button: bubbletea.MouseButtonLeft,
	})
	if !handled || m.sideResize != nil {
		t.Fatalf("release handled=%v preview=%#v", handled, m.sideResize)
	}
	runSettingsCmd(t, saveCmd)
	if len(*edits) != 1 || (*edits)[0].KeyPath != "tui.sidebar_width_percent" {
		t.Fatalf("saved edits = %#v", *edits)
	}
	if !m.sideExactWidth.valid || m.sideExactWidth.width != want {
		t.Fatalf("exact width = %#v, want %d", m.sideExactWidth, want)
	}
}

// Clicking the unfocused pane moves focus there and preserves the split.
func TestSidePaneClickFocusesPaneLikeRust(t *testing.T) {
	m, _ := sidePaneResizeModel(t, 140, false)
	left, _, _, ok := m.sidePaneLayout(m.width)
	if !ok {
		t.Fatal("split layout unavailable")
	}
	handled, _ := m.handleSidePaneMouse(bubbletea.MouseMsg{
		X: left + 5, Y: 2, Action: bubbletea.MouseActionPress, Button: bubbletea.MouseButtonLeft,
	})
	if !handled || !m.activeSide.ShowingSide {
		t.Fatalf("right-pane click handled=%v showingSide=%v", handled, m.activeSide.ShowingSide)
	}
	handled, _ = m.handleSidePaneMouse(bubbletea.MouseMsg{
		X: 2, Y: 2, Action: bubbletea.MouseActionPress, Button: bubbletea.MouseButtonLeft,
	})
	if !handled || m.activeSide.ShowingSide {
		t.Fatalf("left-pane click handled=%v showingSide=%v", handled, m.activeSide.ShowingSide)
	}
}
