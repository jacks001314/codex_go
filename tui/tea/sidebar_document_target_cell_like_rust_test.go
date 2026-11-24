package tea

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	codextui "codex_go/tui"
)

// Rust TranscriptView::render_document fills the target row and toggles
// REVERSED on column_for_offset(target_offset). With a known terminal palette
// the fill is a background colour, so the target cell itself is reversed.
func TestSidebarDocumentTargetCellReversesLikeRust(t *testing.T) {
	restoreColor := codextui.SetStdoutColorLevelForTest(codextui.ColorTrue)
	defer restoreColor()
	restorePalette := codextui.SetDefaultTerminalColorsForTest(&codextui.DefaultColors{
		FG: codextui.RGBColor{R: 220, G: 220, B: 220},
		BG: codextui.RGBColor{R: 10, G: 10, B: 10},
	})
	defer restorePalette()

	model, _ := newSidebarTabsModel(t)
	openDocumentTabAt(t, model, SidebarDocumentView{
		Path:       "/workspace/main.go",
		Text:       "package main\n\nfunc main() {}\n",
		Code:       true,
		Language:   "go",
		LineStarts: []int{0, 13, 14},
		Target:     &SidebarDocumentTarget{Line: 3, Column: 7, Offset: 20},
	})

	row := sidebarDocumentRenderedRow(t, model.renderSidebarDocument(80, 12), "func main")
	if strings.Count(row, "\x1b[7m") != 1 || !strings.Contains(row, "\x1b[7ma\x1b[27m") {
		t.Fatalf("target cell not reversed:\n%q", row)
	}
	// The body is indented one cell, the gutter is two cells (one digit plus
	// a space), and column 7 is the seventh character of the source line, so
	// the reversed cell is the a in main.
	if column := strings.Index(ansi.Strip(row), "a"); column != 9 {
		t.Fatalf("reversed cell column = %d, want 9 in %q", column, ansi.Strip(row))
	}
	// The fill survives the syntax renderer's resets; ratatui composites the
	// row style with every cell, so a reset must not punch a hole in the row.
	fill := codextui.SGRForStyleQuiet(selectionNotUnderlined(codextui.ColorTrue), codextui.ColorTrue)
	if fill == "" || strings.Count(row, fill) < 2 {
		t.Fatalf("target fill %q not restored after resets:\n%q", fill, row)
	}
}

// Without a palette answer Rust falls back to selection_style, which is bold
// and reversed; the target cell then loses the reverse instead of doubling it.
func TestSidebarDocumentTargetCellUnreversesFallbackShadeLikeRust(t *testing.T) {
	restoreColor := codextui.SetStdoutColorLevelForTest(codextui.ColorANSI16)
	defer restoreColor()
	restorePalette := codextui.SetDefaultTerminalColorsForTest(nil)
	defer restorePalette()

	model, _ := newSidebarTabsModel(t)
	openDocumentTabAt(t, model, SidebarDocumentView{
		Path:       "/workspace/main.go",
		Text:       "package main\n\nfunc main() {}\n",
		Code:       true,
		Language:   "go",
		LineStarts: []int{0, 13, 14},
		Target:     &SidebarDocumentTarget{Line: 3, Column: 7, Offset: 20},
	})

	row := sidebarDocumentRenderedRow(t, model.renderSidebarDocument(80, 12), "func main")
	if !strings.HasPrefix(row, " \x1b[1;7m") {
		t.Fatalf("fallback target row is not reversed:\n%q", row)
	}
	if !strings.Contains(row, "\x1b[27ma\x1b[7m") {
		t.Fatalf("target cell did not lose the fallback reverse:\n%q", row)
	}
}

// A long source line is truncated, so a target past the visible width has no
// cell to reverse; Rust skips it with x < text.right().
func TestSidebarDocumentTargetCellPastWidthStaysPlainLikeRust(t *testing.T) {
	restoreColor := codextui.SetStdoutColorLevelForTest(codextui.ColorTrue)
	defer restoreColor()
	restorePalette := codextui.SetDefaultTerminalColorsForTest(&codextui.DefaultColors{
		FG: codextui.RGBColor{R: 220, G: 220, B: 220},
		BG: codextui.RGBColor{R: 10, G: 10, B: 10},
	})
	defer restorePalette()

	model, _ := newSidebarTabsModel(t)
	openDocumentTabAt(t, model, SidebarDocumentView{
		Path:       "/workspace/main.go",
		Text:       "func main() { println(1234567890) }\n",
		Code:       true,
		Language:   "go",
		LineStarts: []int{0},
		Target:     &SidebarDocumentTarget{Line: 1, Column: 30, Offset: 29},
	})

	row := sidebarDocumentRenderedRow(t, model.renderSidebarDocument(20, 10), "func main")
	if strings.Contains(row, "\x1b[7m") {
		t.Fatalf("off-screen target reversed a cell:\n%q", row)
	}
}

// Toggling inside an already reversed run removes the reverse for that cell
// and resumes the run after it, matching Rust's REVERSED toggle.
func TestSidebarDocumentTargetCellTogglesReverseLikeRust(t *testing.T) {
	if got, want := toggleSidebarDocumentReverseCell("\x1b[7mabc\x1b[27m", 1), "\x1b[7ma\x1b[27mb\x1b[7mc\x1b[27m"; got != want {
		t.Fatalf("toggle inside reverse = %q, want %q", got, want)
	}
	// A wide rune owns two cells; column 2 is the second one.
	if got, want := toggleSidebarDocumentReverseCell("代码", 2), "代\x1b[7m码\x1b[27m"; got != want {
		t.Fatalf("toggle on wide rune = %q, want %q", got, want)
	}
	if got, want := toggleSidebarDocumentReverseCell("\x1b[38;5;10mgo\x1b[39m", 1), "\x1b[38;5;10mg\x1b[7mo\x1b[27m\x1b[39m"; got != want {
		t.Fatalf("toggle inside colour = %q, want %q", got, want)
	}
	// Zero channels in a truecolor triplet are colours, not resets.
	if got, want := toggleSidebarDocumentReverseCell("\x1b[38;2;0;0;0mab", 1), "\x1b[38;2;0;0;0ma\x1b[7mb\x1b[27m"; got != want {
		t.Fatalf("toggle after truecolor = %q, want %q", got, want)
	}
}

func sidebarDocumentRenderedRow(t *testing.T, rendered string, needle string) string {
	t.Helper()
	for _, line := range strings.Split(rendered, "\n") {
		if strings.Contains(ansi.Strip(line), needle) {
			return line
		}
	}
	t.Fatalf("row %q missing from preview:\n%s", needle, ansi.Strip(rendered))
	return ""
}
