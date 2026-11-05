package tea

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	codextui "codex_go/tui"
)

// Rust #58992: the side-by-side layout only exists at 100 columns or wider,
// and it splits the remaining width evenly around the divider.
func TestSideWorkspaceWidthsRespectMinimumAndSplitLikeRust(t *testing.T) {
	if _, _, ok := sideWorkspaceWidths(sideWorkspaceMinWidth - 1); ok {
		t.Fatalf("layout enabled below %d columns", sideWorkspaceMinWidth)
	}
	// Rust #59527: the sidebar defaults to one third of the terminal, clamped
	// to its 36-column minimum.
	left, right, ok := sideWorkspaceWidths(100)
	if !ok || left != 61 || right != 36 {
		t.Fatalf("widths at 100 = %d/%d ok=%t", left, right, ok)
	}
	left, right, ok = sideWorkspaceWidths(120)
	if !ok || left != 78 || right != 39 {
		t.Fatalf("widths at 120 = %d/%d ok=%t", left, right, ok)
	}
}

// The panes need the `tui_workspace` feature and an open side conversation.
func TestSidePaneLayoutRequiresFeatureAndOpenSideConversationLikeRust(t *testing.T) {
	side := &activeSideConversation{ParentThreadID: "root", SideThreadID: "side", ShowingSide: true}
	withoutFeature := &Model{activeSide: side}
	if _, _, _, ok := withoutFeature.sidePaneLayout(140); ok {
		t.Fatal("panes rendered without the tui_workspace feature")
	}
	withoutSide := &Model{featureSettings: map[string]bool{sideWorkspaceFeatureKey: true}}
	if _, _, _, ok := withoutSide.sidePaneLayout(140); ok {
		t.Fatal("panes rendered without an open side conversation")
	}
	model := &Model{featureSettings: map[string]bool{sideWorkspaceFeatureKey: true}, activeSide: side}
	left, right, sideFocused, ok := model.sidePaneLayout(140)
	if !ok || !sideFocused || left <= 0 || right <= 0 {
		t.Fatalf("side pane layout = %d/%d focused=%t ok=%t", left, right, sideFocused, ok)
	}
	side.ShowingSide = false
	if _, _, sideFocused, _ := model.sidePaneLayout(140); sideFocused {
		t.Fatal("composer focus followed the wrong conversation")
	}
}

func TestJoinSidePanesKeepsTailAndPadsLikeRust(t *testing.T) {
	joined, _, _, _, _ := joinSidePanes("a\nb\nc", "x\ny", 2, 2, sidePaneTail, sidePaneTail)
	lines := strings.Split(joined, "\n")
	want := []string{"b  │ x", "c  │ y"}
	if len(lines) != len(want) {
		t.Fatalf("rows = %#v", lines)
	}
	for index := range want {
		if lines[index] != want[index] {
			t.Fatalf("row %d = %q, want %q", index, lines[index], want[index])
		}
	}
	if got := ansi.StringWidth(lines[0]); got != 6 {
		t.Fatalf("row width = %d: %q", got, lines[0])
	}
}

// The view paints the parent conversation beside its side conversation and
// keeps every row at the terminal width.
func TestViewRendersSideWorkspacePanesLikeRust(t *testing.T) {
	animations := false
	model := NewModel(codextui.NewState(nil), Options{
		Width:             120,
		Height:            24,
		AnimationsEnabled: &animations,
		FeatureSettings:   map[string]bool{sideWorkspaceFeatureKey: true},
	})
	model.State.ThreadID = "root"
	model.State.Messages = []codextui.Message{{Role: codextui.RoleAssistant, Text: "side conversation answer"}}
	model.activeSide = &activeSideConversation{
		ParentThreadID: "root",
		SideThreadID:   "side",
		ParentMessages: []codextui.Message{{Role: codextui.RoleUser, Text: "parent conversation text"}},
		SideMessages:   []codextui.Message{{Role: codextui.RoleAssistant, Text: "side conversation answer"}},
		ShowingSide:    true,
	}
	model.refreshTranscript()

	view := model.View()
	if !strings.Contains(view, "side conversation answer") {
		t.Fatalf("side transcript missing:\n%s", view)
	}
	if !strings.Contains(view, "parent conversation text") {
		t.Fatalf("parent transcript missing:\n%s", view)
	}
	for index, line := range strings.Split(view, "\n") {
		if width := ansi.StringWidth(line); width != model.width {
			t.Fatalf("row %d width = %d, want %d: %q", index, width, model.width, line)
		}
	}
}
