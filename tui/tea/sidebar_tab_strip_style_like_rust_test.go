package tea

import (
	"strings"
	"testing"

	codextui "codex_go/tui"
)

// sidebarTabStripTruecolor pins a dark truecolor terminal so the strip's
// resolved fills can be compared against Rust's style::shade_rgb arithmetic.
func sidebarTabStripTruecolor(t *testing.T) func() {
	t.Helper()
	restoreColors := codextui.SetDefaultTerminalColorsForTest(&codextui.DefaultColors{
		FG: codextui.RGBColor{R: 230, G: 230, B: 230},
		BG: codextui.RGBColor{R: 16, G: 16, B: 16},
	})
	restoreLevel := codextui.SetStdoutColorLevelForTest(codextui.ColorTrue)
	return func() {
		restoreLevel()
		restoreColors()
	}
}

func newSidebarTabStripStyleModel(t *testing.T) *Model {
	t.Helper()
	model, _ := newSidebarTabsModel(t)
	model.registerSidebarTab(&activeSideConversation{ParentThreadID: "parent-a", SideThreadID: "side-1"})
	model.registerSidebarTab(&activeSideConversation{ParentThreadID: "parent-a", SideThreadID: "side-2"})
	model.sidebarChooserOpen = false
	model.sidebarChooserFocused = false
	model.activeSide = nil
	model.sidebarTabHover = -1
	model.sidebarAddHover = false
	return model
}

// sidebarStripCellSGR returns the SGR prefix painted immediately before cell.
func sidebarStripCellSGR(t *testing.T, strip string, cell string) string {
	t.Helper()
	index := strings.Index(strip, cell)
	if index < 0 {
		t.Fatalf("strip has no %q cell:\n%q", cell, strip)
	}
	start := strings.LastIndex(strip[:index], "\x1b[")
	if start < 0 {
		return ""
	}
	return strip[start:index]
}

// Rust sidebar_tab_strip.rs paints idle tabs on the user-message surface and
// strengthens that surface twice toward the terminal background under the
// pointer instead of reversing the cell.
func TestSidebarTabStripPaintsRustHoverFill(t *testing.T) {
	defer sidebarTabStripTruecolor(t)()
	model := newSidebarTabStripStyleModel(t)

	strip := model.renderSidebarTabStrip(60)
	idle := sidebarStripCellSGR(t, strip, " Side 1 ")
	if !strings.Contains(idle, "48;2;44;44;44") {
		t.Fatalf("idle tab SGR %q did not paint the user-message surface", idle)
	}

	model.sidebarTabHover = 1
	hovered := model.renderSidebarTabStrip(60)
	cell := sidebarStripCellSGR(t, hovered, " Side 2 ")
	if !strings.Contains(cell, "48;2;69;69;69") {
		t.Fatalf("hovered tab SGR %q did not shade the surface twice", cell)
	}
	if strings.Contains(cell, "\x1b[7m") || strings.Contains(cell, ";7") {
		t.Fatalf("hovered tab reversed the cell instead of shading it: %q", cell)
	}
	if other := sidebarStripCellSGR(t, hovered, " Side 1 "); other != idle {
		t.Fatalf("hovering a tab restyled its neighbour: %q vs %q", other, idle)
	}
}

// Rust owned_transcript.rs paints the displayed chat with selection_style and
// leaves the sibling tabs on the quiet surface.
func TestSidebarTabStripPaintsSelectedChatLikeRust(t *testing.T) {
	defer sidebarTabStripTruecolor(t)()
	model := newSidebarTabStripStyleModel(t)
	model.activeSide = &activeSideConversation{
		ParentThreadID: "parent-a",
		SideThreadID:   "side-1",
		ShowingSide:    true,
	}

	strip := model.renderSidebarTabStrip(60)
	selected := sidebarStripCellSGR(t, strip, " Side 1 ")
	if !strings.Contains(selected, "48;2;99;168;248") {
		t.Fatalf("selected tab SGR %q did not keep the selection fill", selected)
	}
	sibling := sidebarStripCellSGR(t, strip, " Side 2 ")
	if !strings.Contains(sibling, "48;2;44;44;44") {
		t.Fatalf("sibling tab SGR %q did not stay on the quiet surface", sibling)
	}

	// A paired-but-hidden chat keeps the hover chip in bold (Rust TabState::Inactive).
	model.activeSide.ShowingSide = false
	hidden := model.renderSidebarTabStrip(60)
	inactive := sidebarStripCellSGR(t, hidden, " Side 1 ")
	if !strings.Contains(inactive, "48;2;69;69;69") || !strings.Contains(inactive, "\x1b[1;") {
		t.Fatalf("hidden paired tab SGR %q is not the bold hover chip", inactive)
	}
}

// Rust paints the focused chooser's "+" with selection_style, the unfocused
// chooser's with quiet_workspace_style, and the side-pane strip's with the
// bare secondary text.
func TestSidebarTabStripPaintsAddButtonStatesLikeRust(t *testing.T) {
	defer sidebarTabStripTruecolor(t)()
	model := newSidebarTabStripStyleModel(t)

	plain := sidebarStripCellSGR(t, model.renderSidebarTabStrip(60), "+")
	if strings.Contains(plain, "48;") {
		t.Fatalf("side-pane add button SGR %q carries a fill", plain)
	}

	model.sidebarChooserOpen = true
	model.sidebarChooserParent = "parent-a"
	model.sidebarChooserFocused = false
	unfocused := sidebarStripCellSGR(t, model.renderSidebarTabStrip(60), "+")
	if !strings.Contains(unfocused, "48;2;27;27;27") {
		t.Fatalf("unfocused chooser add SGR %q is not the quiet workspace fill", unfocused)
	}

	model.sidebarChooserFocused = true
	focused := sidebarStripCellSGR(t, model.renderSidebarTabStrip(60), "+")
	if !strings.Contains(focused, "48;2;99;168;248") {
		t.Fatalf("focused chooser add SGR %q is not the selection fill", focused)
	}
}

// Without a palette the strip falls back to Rust's underline hover and leaves
// idle cells unpainted, so narrow terminals keep their own colours.
func TestSidebarTabStripFallsBackToUnderlineLikeRust(t *testing.T) {
	restoreColors := codextui.SetDefaultTerminalColorsForTest(nil)
	defer restoreColors()
	restoreLevel := codextui.SetStdoutColorLevelForTest(codextui.ColorUnknown)
	defer restoreLevel()

	model := newSidebarTabStripStyleModel(t)
	idle := model.renderSidebarTabStrip(60)
	if strings.Contains(idle, "\x1b[") {
		t.Fatalf("idle strip emitted SGR without a palette:\n%q", idle)
	}

	model.sidebarTabHover = 0
	hovered := model.renderSidebarTabStrip(60)
	if cell := sidebarStripCellSGR(t, hovered, " Side 1 "); cell != "\x1b[4m" {
		t.Fatalf("hovered tab SGR %q is not the underline fallback", cell)
	}
}
