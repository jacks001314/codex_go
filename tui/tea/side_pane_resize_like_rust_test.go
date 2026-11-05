package tea

import (
	"testing"

	bubbletea "github.com/charmbracelet/bubbletea"

	codextui "codex_go/tui"
)

// runSettingsCmd executes a settings write command so its OnWriteSettings
// callback observes the edit.
func runSettingsCmd(t *testing.T, cmd bubbletea.Cmd) {
	t.Helper()
	if cmd == nil {
		t.Fatal("settings write command is nil")
	}
	_ = cmd()
}

func sidePaneResizeModel(t *testing.T, width int, sideFocused bool) (*Model, *[]SettingsEdit) {
	t.Helper()
	var edits []SettingsEdit
	m := NewModel(codextui.NewState(nil), Options{
		Width:  width,
		Height: 30,
		OnWriteSettings: func(writes []SettingsEdit) (SettingsWriteResult, error) {
			edits = append(edits, writes...)
			return SettingsWriteResult{}, nil
		},
	})
	m.featureSettings = map[string]bool{sideWorkspaceFeatureKey: true}
	m.activeSide = &activeSideConversation{
		ParentThreadID: "root",
		SideThreadID:   "side",
		ShowingSide:    sideFocused,
	}
	return m, &edits
}

// Rust #59595: minimize, maximize and equalize assign the focused pane a share
// of the split within the 36/64 column limits and save the sidebar percentage.
func TestSidePaneWidthPresetsLikeRust(t *testing.T) {
	m, edits := sidePaneResizeModel(t, 140, true)
	runSettingsCmd(t, m.setFocusedPaneWidth(0))
	_, side, ok := sideWorkspaceWidthsForPercent(m.width, m.sideWidthPercentValue(), m.sideExactWidth)
	if !ok || side != sideWorkspaceMinSideWidth {
		t.Fatalf("minimized sidebar = %d ok=%t", side, ok)
	}
	runSettingsCmd(t, m.setFocusedPaneWidth(100))
	_, side, ok = sideWorkspaceWidthsForPercent(m.width, m.sideWidthPercentValue(), m.sideExactWidth)
	if !ok {
		t.Fatal("maximized layout unavailable")
	}
	available := m.width - 3
	if side != available-sideWorkspaceMinParentWidth {
		t.Fatalf("maximized sidebar = %d, want %d", side, available-sideWorkspaceMinParentWidth)
	}
	runSettingsCmd(t, m.setFocusedPaneWidth(50))
	left, side, ok := sideWorkspaceWidthsForPercent(m.width, m.sideWidthPercentValue(), m.sideExactWidth)
	// An odd split gives the spare column to the sidebar (Rust #59595).
	if !ok || side != left+1 {
		t.Fatalf("equalized panes = %d/%d ok=%t", left, side, ok)
	}
	if len(*edits) != 3 || (*edits)[2].KeyPath != "tui.sidebar_width_percent" {
		t.Fatalf("saved edits = %#v", *edits)
	}

	// While the parent pane has focus the same presets apply to it.
	parent, _ := sidePaneResizeModel(t, 140, false)
	runSettingsCmd(t, parent.setFocusedPaneWidth(0))
	parentLeft, _, parentOK := sideWorkspaceWidthsForPercent(parent.width, parent.sideWidthPercentValue(), parent.sideExactWidth)
	if !parentOK || parentLeft != sideWorkspaceMinParentWidth {
		t.Fatalf("minimized parent = %d ok=%t", parentLeft, parentOK)
	}
}

// Zoom shows the focused conversation full width and restores the split.
func TestSidePaneZoomLikeRust(t *testing.T) {
	m, _ := sidePaneResizeModel(t, 140, true)
	if _, _, _, ok := m.sidePaneLayout(m.width); !ok {
		t.Fatal("split layout unavailable before zoom")
	}
	m.togglePaneZoom()
	if _, _, _, ok := m.sidePaneLayout(m.width); ok {
		t.Fatal("zoom did not collapse the split")
	}
	m.togglePaneZoom()
	if _, _, _, ok := m.sidePaneLayout(m.width); !ok {
		t.Fatal("zoom off did not restore the split")
	}
}

// Resize mode previews divider movement and saves the width on enter/esc.
func TestSidePaneKeyboardResizeLikeRust(t *testing.T) {
	m, edits := sidePaneResizeModel(t, 140, true)
	m.beginSidePaneResize()
	if m.sideResize == nil {
		t.Fatal("resize mode did not start")
	}
	_, start, _ := sideWorkspaceWidthsForPercent(m.width, m.sideWidthPercentValue(), m.sideExactWidth)
	if m.sideResize.previewWidth != start {
		t.Fatalf("preview = %d, want %d", m.sideResize.previewWidth, start)
	}
	handled, _ := m.handleSidePaneResizeKey(key(bubbletea.KeyLeft))
	if !handled || m.sideResize.previewWidth != start+1 {
		t.Fatalf("left preview = %v/%d, want %d", handled, m.sideResize.previewWidth, start+1)
	}
	m.handleSidePaneResizeKey(key(bubbletea.KeyShiftRight))
	if m.sideResize.previewWidth != start-9 {
		t.Fatalf("shift-right preview = %d, want %d", m.sideResize.previewWidth, start-9)
	}
	m.handleSidePaneResizeKey(configPanelRunes("l"))
	if m.sideResize.previewWidth != start-10 {
		t.Fatalf("l preview = %d, want %d", m.sideResize.previewWidth, start-10)
	}
	handled, saveCmd := m.handleSidePaneResizeKey(key(bubbletea.KeyEnter))
	if !handled {
		t.Fatal("enter was not consumed by resize mode")
	}
	runSettingsCmd(t, saveCmd)
	if m.sideResize != nil {
		t.Fatal("enter did not leave resize mode")
	}
	if len(*edits) != 1 || (*edits)[0].KeyPath != "tui.sidebar_width_percent" {
		t.Fatalf("saved edits = %#v", *edits)
	}
	want := (*edits)[0].Value
	if want != widthPercent(start-10, m.width) {
		t.Fatalf("saved percent = %v, want %d", want, widthPercent(start-10, m.width))
	}
	if !m.sideExactWidth.valid || m.sideExactWidth.width != start-10 {
		t.Fatalf("exact width = %#v", m.sideExactWidth)
	}
}

// A resize preview is discarded by terminal blur or when the split layout is
// no longer available (Rust #59595).
func TestSidePaneResizePreviewCancelledLikeRust(t *testing.T) {
	m, _ := sidePaneResizeModel(t, 140, true)
	m.beginSidePaneResize()
	routeWindowMsg(m, bubbletea.BlurMsg{})
	if m.sideResize != nil {
		t.Fatal("blur kept the resize preview")
	}

	m.beginSidePaneResize()
	m.width = 90
	if _, ok := m.renderSideWorkspace(m.width, 20); ok {
		t.Fatal("narrow terminal still rendered panes")
	}
	if m.sideResize != nil {
		t.Fatal("layout loss kept the resize preview")
	}
}

// The divider nudge only applies while the side pane owns focus and moves by
// 2% of the terminal width.
func TestSidePaneDividerNudgeLikeRust(t *testing.T) {
	side, edits := sidePaneResizeModel(t, 140, true)
	_, before, _ := sideWorkspaceWidthsForPercent(side.width, side.sideWidthPercentValue(), side.sideExactWidth)
	runSettingsCmd(t, side.nudgeSidePaneWidth(2))
	if len(*edits) != 1 {
		t.Fatalf("nudge edits = %#v", *edits)
	}
	_, after, _ := sideWorkspaceWidthsForPercent(side.width, side.sideWidthPercentValue(), side.sideExactWidth)
	if after <= before {
		t.Fatalf("side pane width %d did not grow from %d", after, before)
	}
	parent, parentEdits := sidePaneResizeModel(t, 140, false)
	if cmd := parent.nudgeSidePaneWidth(2); cmd != nil {
		_ = cmd()
	}
	if len(*parentEdits) != 0 {
		t.Fatalf("parent-focused nudge saved %#v", *parentEdits)
	}
}

// The default leader shortcuts reach the pane actions through the normal key
// dispatch (Rust #59595 leader z / , / . / = / r / shift-h / shift-l).
func TestSidePaneLeaderShortcutsLikeRust(t *testing.T) {
	m, _ := sidePaneResizeModel(t, 140, true)
	m.Update(key(bubbletea.KeyCtrlX))
	m.Update(configPanelRunes("z"))
	if !m.sideZoomed {
		t.Fatal("leader z did not zoom the focused pane")
	}
	m.Update(key(bubbletea.KeyCtrlX))
	m.Update(configPanelRunes("z"))
	if m.sideZoomed {
		t.Fatal("leader z did not restore the split")
	}
	m.Update(key(bubbletea.KeyCtrlX))
	m.Update(configPanelRunes(","))
	_, side, ok := sideWorkspaceWidthsForPercent(m.width, m.sideWidthPercentValue(), m.sideExactWidth)
	if !ok || side != sideWorkspaceMinSideWidth {
		t.Fatalf("leader , width = %d ok=%t", side, ok)
	}
	m.Update(key(bubbletea.KeyCtrlX))
	m.Update(configPanelRunes("r"))
	if m.sideResize == nil {
		t.Fatal("leader r did not enter resize mode")
	}
}
