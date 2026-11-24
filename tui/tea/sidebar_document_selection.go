package tea

import (
	"strings"

	bubbletea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	codextui "codex_go/tui"
)

// Rust parity: codex-rs/tui/src/transcript_view/selection.rs as the sidebar
// document reader uses it (#61889). A press starts a selection, a drag extends
// it, and a release copies the text when `copy_on_select` is enabled. Rows and
// columns are body coordinates: row 0 is the first rendered line and column 0
// is its first cell.

type sidebarDocumentSelection struct {
	active    bool
	anchorRow int
	anchorCol int
	endRow    int
	endCol    int
}

func (s *sidebarDocumentSelection) start(row int, column int) {
	s.active = true
	s.anchorRow, s.anchorCol = max(row, 0), max(column, 0)
	s.endRow, s.endCol = s.anchorRow, s.anchorCol
}

func (s *sidebarDocumentSelection) extend(row int, column int) {
	if s == nil || !s.active {
		return
	}
	s.endRow, s.endCol = max(row, 0), max(column, 0)
}

func (s *sidebarDocumentSelection) clear() {
	if s == nil {
		return
	}
	*s = sidebarDocumentSelection{}
}

// empty reports a press that never moved, which Rust treats as a click.
func (s *sidebarDocumentSelection) empty() bool {
	return s == nil || !s.active || s.anchorRow == s.endRow && s.anchorCol == s.endCol
}

// normalized orders the anchor and the drag end the way text extraction walks
// them: top row first, and left column first on a single row.
func (s *sidebarDocumentSelection) normalized() (int, int, int, int) {
	startRow, startCol := s.anchorRow, s.anchorCol
	endRow, endCol := s.endRow, s.endCol
	if startRow > endRow || startRow == endRow && startCol > endCol {
		startRow, endRow = endRow, startRow
		startCol, endCol = endCol, startCol
	}
	return startRow, startCol, endRow, endCol
}

// spanFor returns the selected display columns of one rendered row.
func (s *sidebarDocumentSelection) spanFor(row int, width int) (int, int, bool) {
	if s == nil || !s.active || width <= 0 {
		return 0, 0, false
	}
	startRow, startCol, endRow, endCol := s.normalized()
	if row < startRow || row > endRow {
		return 0, 0, false
	}
	from, to := 0, width
	if row == startRow {
		from = startCol
	}
	if row == endRow {
		to = endCol + 1
	}
	from = clampModalInt(from, 0, width)
	to = clampModalInt(to, 0, width)
	if to <= from {
		return 0, 0, false
	}
	return from, to, true
}

// startSidebarDocumentSelection anchors a drag at the pointer, like Rust's
// Begin(MouseButton::Left) handling in select mode.
func (m *Model) startSidebarDocumentSelection(tab *sidebarDocumentTab, row int, column int) {
	if m == nil || tab == nil {
		return
	}
	tab.Link = -1
	tab.HoverLink = -1
	tab.Selection.start(row, column)
	m.refreshTranscript()
}

// extendSidebarDocumentSelection follows the pointer while the drag is live.
func (m *Model) extendSidebarDocumentSelection(tab *sidebarDocumentTab, y int, height int, column int) {
	if m == nil || tab == nil || !tab.Selection.active {
		return
	}
	row, ok := m.sidebarDocumentPointerRow(tab, y, height)
	if !ok {
		// Dragging past the body clamps to the nearest rendered row so the
		// selection still follows the pointer.
		if y <= 1 {
			row = tab.Scroll
		} else {
			row = tab.Scroll + max(height-3, 0)
		}
	}
	tab.Selection.extend(row, column)
	m.refreshTranscript()
}

// finishSidebarDocumentSelection copies a completed drag when copy_on_select is
// enabled, and clears the selection when the mode asks for it.
func (m *Model) finishSidebarDocumentSelection(tab *sidebarDocumentTab) bubbletea.Cmd {
	if m == nil || tab == nil || !tab.Selection.active {
		return nil
	}
	if tab.Selection.empty() {
		tab.Selection.clear()
		m.refreshTranscript()
		return nil
	}
	if !m.copyOnSelectEnabled() {
		return nil
	}
	cmd := m.copySidebarDocumentSelection(tab, true)
	if m.copyOnSelectClear {
		tab.Selection.clear()
		m.refreshTranscript()
	}
	return cmd
}

// copySidebarDocumentSelection puts the selected cells on the clipboard and,
// for copy-on-select, publishes them to PRIMARY under this preview's claim
// (Rust TranscriptView::copy_selection plus Sidebar::cancel_hidden_document_primary).
func (m *Model) copySidebarDocumentSelection(tab *sidebarDocumentTab, primary bool) bubbletea.Cmd {
	text := m.sidebarDocumentSelectionText(tab)
	if strings.TrimSpace(text) == "" {
		return nil
	}
	clipboardCmd := m.clipboardWriteCmd("document selection", text, false, false)
	if !primary {
		return clipboardCmd
	}
	return bubbletea.Batch(clipboardCmd, m.publishSidebarDocumentPrimaryCmd(tab, text))
}

// sidebarDocumentSelectionText extracts the selected text from the rendered
// rows, trimming the trailing padding every row carries.
func (m *Model) sidebarDocumentSelectionText(tab *sidebarDocumentTab) string {
	if m == nil || tab == nil || !tab.Selection.active {
		return ""
	}
	lines := m.sidebarDocumentLines(tab, m.sidebarDocumentBodyWidth())
	startRow, startCol, endRow, endCol := tab.Selection.normalized()
	startRow = max(startRow, 0)
	endRow = min(endRow, len(lines)-1)
	if endRow < startRow {
		return ""
	}
	out := make([]string, 0, endRow-startRow+1)
	for row := startRow; row <= endRow; row++ {
		plain := ansi.Strip(lines[row])
		from, to := 0, codextui.DisplayWidth(plain)
		if row == startRow {
			from = startCol
		}
		if row == endRow {
			to = endCol + 1
		}
		out = append(out, strings.TrimRight(sliceDisplayCells(plain, from, to), " "))
	}
	return strings.TrimRight(strings.Join(out, "\n"), "\n")
}
