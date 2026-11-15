package chatwidget

import (
	"sort"
	"strings"

	"github.com/mattn/go-runewidth"
)

// Rust parity: codex-rs/tui/src/transcript_view/select_mode/table.rs and
// transcript_view/text.rs (`table_cell_at`, #61380/#61422).
//
// The Rust view keeps table metadata on its text layout. This pager keeps the
// transcript as rendered plain rows, so it recovers the cell grid from the
// compact table renderer: the header rule is drawn with U+2501 and body rules
// with U+2500 (tui/markdown/table.go), and every run marks one padded column.

// tableCellPosition identifies one cell in the transcript table grid.
type tableCellPosition struct {
	Table int
	Row   int
	Col   int
}

// tableCellSpan is the half-open display-column span of one padded cell.
type tableCellSpan struct {
	Start int
	End   int
}

// tableRowLayout maps one rendered row onto its cell spans.
type tableRowLayout struct {
	Line  int
	Cells []tableCellSpan
}

// tableLayout is one rendered markdown table.
type tableLayout struct {
	Rows []tableRowLayout
	// Rules maps a separator line to the row it follows, so pointer drags that
	// cross a rule keep extending the same cell rectangle.
	Rules      map[int]int
	Alignments []string
}

func (t tableLayout) columns() int {
	if len(t.Rows) == 0 {
		return 0
	}
	return len(t.Rows[0].Cells)
}

// tableRuleSpans parses a table rule row into its padded cell spans. A rule row
// is nothing but runs of rule separated by the renderer's two-space gap.
func tableRuleSpans(line string, rule rune) ([]tableCellSpan, bool) {
	var spans []tableCellSpan
	column := 0
	gap := 0
	seenRule := false
	inRun := false
	for _, r := range line {
		width := runewidth.RuneWidth(r)
		if width <= 0 {
			width = 1
		}
		switch {
		case r == rule:
			if seenRule && !inRun && gap != 2 {
				return nil, false
			}
			gap = 0
			if inRun {
				spans[len(spans)-1].End = column + width
			} else {
				spans = append(spans, tableCellSpan{Start: column, End: column + width})
				inRun = true
			}
			seenRule = true
		case r == ' ':
			if seenRule {
				gap += width
				if gap > 2 {
					return nil, false
				}
				inRun = false
			}
		default:
			return nil, false
		}
		column += width
	}
	if len(spans) == 0 || gap != 0 {
		return nil, false
	}
	return spans, true
}

// buildTableLayouts finds rendered tables. A header rule (U+2501) opens a
// table whose header row is the line above it; body rows follow until a blank
// line, with light rules (U+2500) between them.
func buildTableLayouts(lines []string) []tableLayout {
	var tables []tableLayout
	for index := 1; index < len(lines); index++ {
		spans, ok := tableRuleSpans(lines[index], '\u2501')
		if !ok || len(spans) == 0 || strings.TrimSpace(lines[index-1]) == "" {
			continue
		}
		if _, isRule := tableRuleSpans(lines[index-1], '\u2500'); isRule {
			continue
		}
		table := tableLayout{
			Rows:  []tableRowLayout{{Line: index - 1, Cells: spans}},
			Rules: map[int]int{index: 0},
		}
		next := index + 1
		for next < len(lines) {
			line := lines[next]
			if strings.TrimSpace(line) == "" {
				break
			}
			if _, bodyRule := tableRuleSpans(line, '\u2500'); bodyRule {
				table.Rules[next] = len(table.Rows) - 1
				next++
				continue
			}
			if _, headerRule := tableRuleSpans(line, '\u2501'); headerRule {
				break
			}
			if !tableRowFits(line, spans) {
				break
			}
			table.Rows = append(table.Rows, tableRowLayout{Line: next, Cells: spans})
			next++
		}
		table.Alignments = inferTableAlignments(lines, table)
		tables = append(tables, table)
		index = next - 1
	}
	return tables
}

// tableRowFits reports whether a rendered row belongs to the grid described by
// spans. Non-final cells are padded to their span, so a row can never extend
// past the last column.
func tableRowFits(line string, spans []tableCellSpan) bool {
	if strings.TrimSpace(line) == "" {
		return false
	}
	return runewidth.StringWidth(line) <= spans[len(spans)-1].End
}

// cellTextBounds returns the display columns of the first and last non-space
// rune of span on line.
func cellTextBounds(line string, span tableCellSpan) (int, int, bool) {
	start, end := -1, -1
	column := 0
	for _, r := range line {
		width := runewidth.RuneWidth(r)
		if width <= 0 {
			width = 1
		}
		if column >= span.Start && column < span.End && r != ' ' {
			if start < 0 {
				start = column
			}
			end = column + width
		}
		column += width
		if column >= span.End {
			break
		}
	}
	if start < 0 || end <= start {
		return 0, 0, false
	}
	return start, end, true
}

// inferTableAlignments rebuilds the markdown alignment row. The rendered grid
// only shows padding, so the padding distribution stands in for the source
// alignment markers.
func inferTableAlignments(lines []string, table tableLayout) []string {
	columns := table.columns()
	alignments := make([]string, columns)
	for column := 0; column < columns; column++ {
		leftPadded := false
		rightPadded := false
		sawCell := false
		for _, row := range table.Rows {
			if column >= len(row.Cells) || row.Line < 0 || row.Line >= len(lines) {
				continue
			}
			span := row.Cells[column]
			start, end, ok := cellTextBounds(lines[row.Line], span)
			if !ok {
				continue
			}
			sawCell = true
			if start-span.Start > 1 {
				leftPadded = true
			}
			if span.End-end > 1 {
				rightPadded = true
			}
		}
		switch {
		case !sawCell:
			alignments[column] = "---"
		case leftPadded && rightPadded:
			alignments[column] = ":---:"
		case leftPadded:
			alignments[column] = "---:"
		default:
			alignments[column] = "---"
		}
	}
	return alignments
}

// tableCellAt maps a caret or pointer position onto a table cell. Points in a
// row's trailing padding belong to the last cell, matching Rust's grid hit.
func (o *TranscriptOverlay) tableCellAt(point selectPoint) (tableCellPosition, bool) {
	if o == nil || point.Row < 0 {
		return tableCellPosition{}, false
	}
	for index := range o.tables {
		table := o.tables[index]
		for rowIndex, row := range table.Rows {
			if row.Line != point.Row {
				continue
			}
			if column, ok := tableCellColumn(o, row, point.Col); ok {
				return tableCellPosition{Table: index, Row: rowIndex, Col: column}, true
			}
		}
		if rowIndex, ok := table.Rules[point.Row]; ok && rowIndex < len(table.Rows) {
			if column, ok := tableCellColumn(o, table.Rows[rowIndex], point.Col); ok {
				return tableCellPosition{Table: index, Row: rowIndex, Col: column}, true
			}
		}
	}
	return tableCellPosition{}, false
}

// tableCellColumn maps a display column onto a cell index of row.
func tableCellColumn(o *TranscriptOverlay, row tableRowLayout, column int) (int, bool) {
	if len(row.Cells) > 0 && column < row.Cells[0].Start {
		// Row prefix/padding belongs to the first cell, like Rust's grid hit.
		return 0, true
	}
	for index, span := range row.Cells {
		if column >= span.Start && column < span.End {
			return index, true
		}
	}
	last := len(row.Cells) - 1
	if last >= 0 {
		width := o.lineWidth(row.Line)
		if column >= width && column < row.Cells[last].End {
			return last, true
		}
	}
	return 0, false
}

func (o *TranscriptOverlay) tableCellRow(position tableCellPosition) (tableRowLayout, bool) {
	if o == nil || position.Table < 0 || position.Table >= len(o.tables) {
		return tableRowLayout{}, false
	}
	table := o.tables[position.Table]
	if position.Row < 0 || position.Row >= len(table.Rows) {
		return tableRowLayout{}, false
	}
	row := table.Rows[position.Row]
	if position.Col < 0 || position.Col >= len(row.Cells) {
		return tableRowLayout{}, false
	}
	return row, true
}

// tableCellPoint returns the first text column of a cell so cell-wise motions
// keep the caret on visible content.
func (o *TranscriptOverlay) tableCellPoint(position tableCellPosition) selectPoint {
	row, ok := o.tableCellRow(position)
	if !ok {
		return selectPoint{}
	}
	span := row.Cells[position.Col]
	column := span.Start
	if start, _, ok := cellTextBounds(o.plainLines[row.Line], span); ok {
		column = start
	}
	return selectPoint{Row: row.Line, Col: column}
}

// tableCellTextSpan returns the marked range of one cell's text.
func (o *TranscriptOverlay) tableCellTextSpan(position tableCellPosition) (selectPoint, selectPoint, bool) {
	row, ok := o.tableCellRow(position)
	if !ok {
		return selectPoint{}, selectPoint{}, false
	}
	span := row.Cells[position.Col]
	start, end, ok := cellTextBounds(o.plainLines[row.Line], span)
	if !ok {
		return selectPoint{Row: row.Line, Col: span.Start}, selectPoint{Row: row.Line, Col: span.Start}, true
	}
	return selectPoint{Row: row.Line, Col: start}, selectPoint{Row: row.Line, Col: end}, true
}

// tableSelectionRect normalizes the marked cell rectangle.
func (o *TranscriptOverlay) tableSelectionRect() (tableLayout, int, int, int, int, bool) {
	if o == nil || !o.selectMode.tableMark || !o.selectMode.marked {
		return tableLayout{}, 0, 0, 0, 0, false
	}
	anchor := o.selectMode.anchorCell
	active := o.selectMode.activeCell
	if anchor.Table < 0 || anchor.Table >= len(o.tables) || anchor.Table != active.Table {
		return tableLayout{}, 0, 0, 0, 0, false
	}
	table := o.tables[anchor.Table]
	if anchor.Row < 0 || anchor.Row >= len(table.Rows) || active.Row < 0 || active.Row >= len(table.Rows) {
		return tableLayout{}, 0, 0, 0, 0, false
	}
	minRow, maxRow := anchor.Row, active.Row
	if minRow > maxRow {
		minRow, maxRow = maxRow, minRow
	}
	minCol, maxCol := anchor.Col, active.Col
	if minCol > maxCol {
		minCol, maxCol = maxCol, minCol
	}
	return table, minRow, maxRow, minCol, maxCol, true
}

// tableSelectionSpan returns the highlighted columns of one table row.
func (o *TranscriptOverlay) tableSelectionSpan(line int) (int, int, bool) {
	table, minRow, maxRow, minCol, maxCol, ok := o.tableSelectionRect()
	if !ok {
		return 0, 0, false
	}
	for rowIndex := minRow; rowIndex <= maxRow; rowIndex++ {
		row := table.Rows[rowIndex]
		if row.Line != line {
			continue
		}
		if minCol >= len(row.Cells) {
			return 0, 0, false
		}
		lastCol := maxCol
		if lastCol >= len(row.Cells) {
			lastCol = len(row.Cells) - 1
		}
		from := row.Cells[minCol].Start
		to := row.Cells[lastCol].End
		width := o.lineWidth(line)
		from = clampColumn(from, width)
		to = clampColumn(to, width)
		if to <= from {
			return 0, 0, false
		}
		return from, to, true
	}
	return 0, 0, false
}

// selectedTableText copies a marked cell rectangle the way Rust's
// markdown_copy table renderer does: a markdown table whose header row is
// always present (empty when unselected) followed by the selected rows.
func (o *TranscriptOverlay) selectedTableText() string {
	table, minRow, maxRow, minCol, maxCol, ok := o.tableSelectionRect()
	if !ok {
		return ""
	}
	if minRow == maxRow && minCol == maxCol {
		start, end, ok := o.tableCellTextSpan(o.selectMode.activeCell)
		if !ok {
			return ""
		}
		return sliceByDisplayColumns(o.plainLines[start.Row], start.Col, end.Col)
	}
	columns := table.columns()
	values := make(map[int][]string, maxRow-minRow+2)
	values[0] = make([]string, columns)
	for row := minRow; row <= maxRow; row++ {
		values[row] = make([]string, columns)
	}
	for row := minRow; row <= maxRow; row++ {
		layout := table.Rows[row]
		for column := minCol; column <= maxCol && column < len(layout.Cells); column++ {
			start, end, ok := cellTextBounds(o.plainLines[layout.Line], layout.Cells[column])
			if !ok {
				continue
			}
			text := strings.TrimSpace(sliceByDisplayColumns(o.plainLines[layout.Line], start, end))
			values[row][column] = escapeMarkdownTableCell(text)
		}
	}
	rows := make([]int, 0, len(values))
	for row := range values {
		rows = append(rows, row)
	}
	sort.Ints(rows)
	lines := make([]string, 0, len(rows)+1)
	for _, row := range rows {
		lines = append(lines, "| "+strings.Join(values[row], " | ")+" |")
		if row == 0 {
			alignments := table.Alignments
			if len(alignments) == 0 {
				alignments = make([]string, columns)
				for index := range alignments {
					alignments[index] = "---"
				}
			}
			lines = append(lines, "|"+strings.Join(alignments, "|")+"|")
		}
	}
	return strings.Join(lines, "\n")
}

func escapeMarkdownTableCell(text string) string {
	text = strings.ReplaceAll(text, "\\", "\\\\")
	return strings.ReplaceAll(text, "|", "\\|")
}
