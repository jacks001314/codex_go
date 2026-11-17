package tea

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	codextui "codex_go/tui"
)

// Rust #58992/#61141: the split frame is two complete conversations. The
// focused column keeps its own composer, status line and footer, and the side
// pane keeps its tab strip on the first row in either focus state.
func TestSideWorkspacePanesOwnTheirBottomPaneLikeRust(t *testing.T) {
	model, _ := newSidebarTabsModel(t)
	openSideForTest(t, model)
	model.State.AddMessage(codextui.RoleAssistant, "side answer text")
	model.refreshTranscript()

	rightStart := func() int {
		t.Helper()
		left, _, _, ok := model.sidePaneLayout(model.width)
		if !ok {
			t.Fatal("split layout unavailable")
		}
		return left + ansi.StringWidth(sideWorkspaceDivider)
	}
	rowWith := func(rows []string, needle string) string {
		t.Helper()
		for _, row := range rows {
			if strings.Contains(ansi.Strip(row), needle) {
				return ansi.Strip(row)
			}
		}
		t.Fatalf("view is missing %q:\n%s", needle, strings.Join(rows, "\n"))
		return ""
	}

	view := model.viewString()
	rows := strings.Split(view, "\n")
	for index, row := range rows {
		if width := ansi.StringWidth(row); width != model.width {
			t.Fatalf("row %d width = %d, want %d: %q", index, width, model.width, ansi.Strip(row))
		}
	}
	// The side conversation owns the composer, so its prompt sits past the
	// divider and the parent half of the row stays blank. Rust swaps in
	// SIDE_PLACEHOLDER while the side chat owns the bottom pane.
	prompt := rowWith(rows, "› "+SideComposerPlaceholder)
	divider := strings.Index(prompt, "│")
	if divider < 0 {
		t.Fatalf("composer row has no divider: %q", prompt)
	}
	// The panes size their own bottom stacks, so the parked column may paint a
	// status row beside the focused composer; the parent half must still never
	// paint a second prompt.
	if strings.Contains(prompt[:divider], "\u203a") {
		t.Fatalf("focused side composer spilled into the parent column: %q", prompt)
	}
	if !strings.Contains(prompt[divider:], "› "+SideComposerPlaceholder) {
		t.Fatalf("focused side composer missing from its column: %q", prompt)
	}
	if !strings.Contains(ansi.Strip(rows[0])[rightStart():], sidebarSingleTabCell) {
		t.Fatalf("side pane tab strip missing from the first row: %q", ansi.Strip(rows[0]))
	}

	// Switching to the parent moves the composer into the left column while the
	// side pane keeps its tab strip (Rust paints the Inactive state too).
	model.focusMainComposer()
	model.composer.SetValue("parent draft")
	rows = strings.Split(model.viewString(), "\n")
	for index, row := range rows {
		if width := ansi.StringWidth(row); width != model.width {
			t.Fatalf("row %d width = %d, want %d: %q", index, width, model.width, ansi.Strip(row))
		}
	}
	prompt = rowWith(rows, "› parent draft")
	divider = strings.Index(prompt, "│")
	if divider < 0 || !strings.Contains(prompt[:divider], "› parent draft") {
		t.Fatalf("focused parent composer left its column: %q", prompt)
	}
	if !strings.Contains(ansi.Strip(rows[0])[rightStart():], sidebarSingleTabCell) {
		t.Fatalf("inactive side pane lost its tab strip: %q", ansi.Strip(rows[0]))
	}
}
