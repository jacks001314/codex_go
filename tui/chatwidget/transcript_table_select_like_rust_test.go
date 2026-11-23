package chatwidget

import (
	"strings"
	"testing"

	"codex_go/tui/markdown"
	"codex_go/tui/mousemsg"
	"codex_go/utils"
)

// Rust parity: codex-rs/tui/src/transcript_view/select_mode/table.rs and
// transcript_view/table_copy_tests.rs (#61380/#61422).

const tableSelectSource = "| Task | Status |\n|---|---|\n| One | Done |\n| Two | Open |"

func tableSelectContent(t *testing.T, source string) string {
	t.Helper()
	rendered, err := markdown.RenderUniformListsWithThemeCwd(source, 80, "", "")
	if err != nil {
		t.Fatalf("RenderUniformListsWithThemeCwd() error = %v", err)
	}
	lines := strings.Split(strings.TrimRight(rendered, "\r\n"), "\n")
	for index, line := range lines {
		text := strings.TrimRight(utils.StripANSI(line), " ")
		if text != "" {
			text = "  " + text
		}
		lines[index] = text
	}
	return strings.Join(lines, "\n")
}

func newTableSelectOverlay(t *testing.T, source string) *TranscriptOverlay {
	t.Helper()
	overlay := NewTranscriptOverlay(90, 24, tableSelectContent(t, source))
	overlay.BeginSelection()
	if !overlay.SelectionActive() {
		t.Fatal("selection mode did not start")
	}
	return overlay
}

func TestTableLayoutRecoveredFromRenderedGridLikeRust(t *testing.T) {
	overlay := NewTranscriptOverlay(90, 24, tableSelectContent(t, tableSelectSource))
	if len(overlay.tables) != 1 {
		t.Fatalf("tables = %d, want 1:\n%s", len(overlay.tables), overlay.content)
	}
	table := overlay.tables[0]
	if table.columns() != 2 || len(table.Rows) != 3 {
		t.Fatalf("table = %d columns, %d rows; want 2 columns, 3 rows", table.columns(), len(table.Rows))
	}
	if got := table.Alignments; len(got) != 2 || got[0] != "---" || got[1] != "---" {
		t.Fatalf("alignments = %#v", got)
	}
}

func TestTableAlignmentsRecoveredFromPaddingLikeRust(t *testing.T) {
	overlay := NewTranscriptOverlay(90, 24, tableSelectContent(t, "| A | B |\n|---|----:|\n| 1 | 22 |\n| 2 | 3 |"))
	if len(overlay.tables) != 1 {
		t.Fatalf("tables = %d, want 1:\n%s", len(overlay.tables), overlay.content)
	}
	if got := overlay.tables[0].Alignments; len(got) != 2 || got[0] != "---" || got[1] != "---:" {
		t.Fatalf("alignments = %#v, want [--- ---:]", got)
	}
}

func TestMarkMarkdownMarksCellAndRectangleLikeRust(t *testing.T) {
	overlay := newTableSelectOverlay(t, tableSelectSource)
	cell := tableCellPosition{Table: 0, Row: 2, Col: 1}
	overlay.selectMode.anchor = overlay.tableCellPoint(cell)
	overlay.selectMode.activeEnd = overlay.selectMode.anchor

	if action := overlay.HandleSelectCommand(SelectCmdMarkMarkdown); action != SelectActionChanged {
		t.Fatalf("mark table action = %v", action)
	}
	if !overlay.SelectionMarked() {
		t.Fatal("cell mark did not mark the selection")
	}
	if got := overlay.SelectedText(); got != "Open" {
		t.Fatalf("single cell copy = %q, want %q", got, "Open")
	}

	// Extending upwards marks the rectangle of both body rows in that column;
	// Rust always reconstructs the (here empty) markdown header row.
	if action := overlay.HandleSelectCommand(SelectCmdUp); action != SelectActionChanged {
		t.Fatalf("extend cell action = %v", action)
	}
	want := "|  |  |\n|---|---|\n|  | Done |\n|  | Open |"
	if got := overlay.SelectedText(); got != want {
		t.Fatalf("rectangle copy = %q, want %q", got, want)
	}

	// `v` converts the cell mark into the active cell's text mark.
	overlay.HandleSelectCommand(SelectCmdMarkCharacter)
	if got := overlay.SelectedText(); got != "Done" {
		t.Fatalf("cell text copy = %q, want %q", got, "Done")
	}
	overlay.HandleSelectCommand(SelectCmdMarkCharacter)
	if got := overlay.SelectedText(); got != "" {
		t.Fatalf("second cell-text mark kept selection: %q", got)
	}
}

func TestMarkMarkdownTogglesOffLikeRust(t *testing.T) {
	overlay := newTableSelectOverlay(t, tableSelectSource)
	cell := tableCellPosition{Table: 0, Row: 1, Col: 0}
	overlay.selectMode.anchor = overlay.tableCellPoint(cell)
	overlay.selectMode.activeEnd = overlay.selectMode.anchor
	overlay.HandleSelectCommand(SelectCmdMarkMarkdown)
	if !overlay.SelectionMarked() {
		t.Fatal("cell mark did not mark the selection")
	}
	overlay.HandleSelectCommand(SelectCmdMarkMarkdown)
	if overlay.SelectionMarked() || overlay.SelectedText() != "" {
		t.Fatalf("second mark table did not clear mark: marked=%v text=%q", overlay.SelectionMarked(), overlay.SelectedText())
	}
}

func TestTableCellMotionStaysInsideTableLikeRust(t *testing.T) {
	overlay := newTableSelectOverlay(t, tableSelectSource)
	cell := tableCellPosition{Table: 0, Row: 2, Col: 1}
	overlay.selectMode.anchor = overlay.tableCellPoint(cell)
	overlay.selectMode.activeEnd = overlay.selectMode.anchor
	overlay.HandleSelectCommand(SelectCmdMarkMarkdown)

	if action := overlay.HandleSelectCommand(SelectCmdDown); action != SelectActionNone {
		t.Fatalf("down past the last row action = %v, want none", action)
	}
	if action := overlay.HandleSelectCommand(SelectCmdRight); action != SelectActionNone {
		t.Fatalf("right past the last column action = %v, want none", action)
	}
	if action := overlay.HandleSelectCommand(SelectCmdLeft); action != SelectActionChanged {
		t.Fatalf("left action = %v", action)
	}
	if got := overlay.SelectedText(); got != "|  |  |\n|---|---|\n| Two | Open |" {
		t.Fatalf("row rectangle copy = %q", got)
	}
}

func TestMouseDragSelectsTableCellRectangleLikeRust(t *testing.T) {
	overlay := NewTranscriptOverlay(90, 24, tableSelectContent(t, tableSelectSource))
	table := overlay.tables[0]
	from := table.Rows[1]
	to := table.Rows[2]
	press := mousemsg.MouseMsg{
		Action: mousemsg.MouseActionPress,
		Button: mousemsg.MouseButtonLeft,
		X:      from.Cells[0].Start + 1,
		Y:      from.Line + 1 + overlay.headerRows(),
	}
	overlay.Update(press)
	if !overlay.dragActive {
		t.Fatal("press inside a table cell did not start a cell drag")
	}
	motion := press
	motion.Action = mousemsg.MouseActionMotion
	motion.X = to.Cells[1].Start + 1
	motion.Y = to.Line + 1 + overlay.headerRows()
	overlay.Update(motion)
	want := "|  |  |\n|---|---|\n| One | Done |\n| Two | Open |"
	if got := overlay.SelectedText(); got != want {
		t.Fatalf("mouse rectangle copy = %q, want %q", got, want)
	}
}
