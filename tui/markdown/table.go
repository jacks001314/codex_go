package markdown

import (
	"regexp"
	"strconv"
	"strings"

	codextui "codex_go/tui"
	"codex_go/utils"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	gmtext "github.com/yuin/goldmark/text"
)

// Rust parity: codex-rs/tui/src/markdown_render.rs table rendering.
//
// The Go TUI renders markdown through glamour, whose tables are laid out with
// a full-width column spread and ASCII/pipe separators. The Rust codex TUI
// instead renders tables with box-drawing separators and compact natural
// column widths. These helpers detect markdown tables in the source, render
// them in the compact Rust style, and splice the result back into the glamour
// output so the dialogue area matches the Rust rendering.

const (
	tableHeaderSeparatorChar = "\u2501" // ━ heavy horizontal
	tableBodySeparatorChar   = "\u2500" // ─ light horizontal
	tableColumnGap           = 2
	tableCellPadding         = 1
	minTableColumnWidth      = 3
	tableMarkerPrefix        = "CODEX_INTERNAL_TABLE_"
)

var (
	tableLinkRE  = regexp.MustCompile(`^\[([^\]]*)\]\([^)]*\)$`)
	tableImageRE = regexp.MustCompile(`^!\[([^\]]*)\]\([^)]*\)$`)
)

type sourceTable struct {
	id         int
	alignments []string
	header     []string
	rows       [][]string
	blockquote bool
}

// detectSourceTables scans markdown source for table blocks (a header row
// followed by a delimiter row and optional body rows), records them, and
// returns the source with each table replaced by a single marker line. Tables
// inside fenced code blocks are left untouched.
func detectSourceTables(source string) ([]sourceTable, string) {
	lines := strings.Split(strings.ReplaceAll(source, "\r\n", "\n"), "\n")
	var tables []sourceTable
	out := make([]string, 0, len(lines))
	fence := codextui.NewFenceTracker()
	tableID := 0

	for i := 0; i < len(lines); i++ {
		line := lines[i]
		fence.Advance(line)
		if fence.Kind() != codextui.FenceOutside {
			out = append(out, line)
			continue
		}
		// Blockquote tables: lines prefixed with ">" that contain a table.
		if isBlockquoteTableLine(line) && i+1 < len(lines) {
			bqHeader, _ := stripBlockquoteTablePrefix(line)
			bqNext, nextOK := stripBlockquoteTablePrefix(lines[i+1])
			if nextOK && codextui.IsTableHeaderLine(bqHeader) && codextui.IsTableDelimiterLine(bqNext) {
				header, _ := codextui.ParseTableSegments(bqHeader)
				alignments := parseTableAlignments(bqNext)
				t := sourceTable{id: tableID, alignments: alignments, header: header, blockquote: true}
				tableID++
				i += 2
				for i < len(lines) {
					row, ok := stripBlockquoteTablePrefix(lines[i])
					if !ok || strings.TrimSpace(row) == "" || !isTableRowLine(row) {
						break
					}
					cells, _ := codextui.ParseTableSegments(row)
					t.rows = append(t.rows, cells)
					i++
				}
				tables = append(tables, t)
				out = append(out, "> "+tableMarkerLine(t.id))
				continue
			}
		}
		if isTableCandidateLine(line) && i+1 < len(lines) &&
			codextui.IsTableHeaderLine(line) && codextui.IsTableDelimiterLine(lines[i+1]) {
			header, _ := codextui.ParseTableSegments(line)
			alignments := parseTableAlignments(lines[i+1])
			t := sourceTable{id: tableID, alignments: alignments, header: header}
			tableID++
			i += 2
			for i < len(lines) {
				row := lines[i]
				if strings.TrimSpace(row) == "" || !isTableCandidateLine(row) || !isTableRowLine(row) {
					break
				}
				cells, _ := codextui.ParseTableSegments(row)
				t.rows = append(t.rows, cells)
				i++
			}
			tables = append(tables, t)
			out = append(out, "")
			out = append(out, tableMarkerLine(t.id))
			out = append(out, "")
			continue
		}
		out = append(out, line)
	}
	return tables, strings.Join(out, "\n")
}

// isBlockquoteTableLine reports whether a source line is a blockquote table row
// (starts with ">" followed by optional whitespace and a pipe table line).
func isBlockquoteTableLine(line string) bool {
	_, ok := stripBlockquoteTablePrefix(line)
	return ok
}

func stripBlockquoteTablePrefix(line string) (string, bool) {
	trimmed := strings.TrimSpace(line)
	if !strings.HasPrefix(trimmed, ">") {
		return "", false
	}
	content := strings.TrimLeft(strings.TrimPrefix(trimmed, ">"), " ")
	if content == "" || content == "> " {
		return "", false
	}
	return content, true
}

// isTableCandidateLine reports whether a source line can start a table row.
// It excludes indented code lines and blockquotes, which the simple line-based
// table scanner would otherwise misclassify as table headers.
func isTableCandidateLine(line string) bool {
	if strings.HasPrefix(line, ">") {
		return false
	}
	leadingSpaces := 0
	for leadingSpaces < len(line) && line[leadingSpaces] == ' ' {
		leadingSpaces++
	}
	return leadingSpaces < 4
}

func tableMarkerLine(id int) string {
	return "<" + tableMarkerPrefix + strconv.Itoa(id) + ">"
}

func isTableRowLine(line string) bool {
	segments, ok := codextui.ParseTableSegments(line)
	if !ok {
		return false
	}
	for _, segment := range segments {
		if strings.TrimSpace(segment) != "" {
			return true
		}
	}
	return false
}

func parseTableAlignments(delimiterLine string) []string {
	segments, ok := codextui.ParseTableSegments(delimiterLine)
	if !ok {
		return nil
	}
	alignments := make([]string, len(segments))
	for i, segment := range segments {
		seg := strings.TrimSpace(segment)
		alignments[i] = "none"
		switch {
		case strings.HasPrefix(seg, ":") && strings.HasSuffix(seg, ":"):
			alignments[i] = "center"
		case strings.HasSuffix(seg, ":"):
			alignments[i] = "right"
		case strings.HasPrefix(seg, ":"):
			alignments[i] = "left"
		}
	}
	return alignments
}

// restoreRenderedTables replaces each marker line in the glamour output with
// the compact Rust-style rendered table.
func restoreRenderedTables(rendered string, tables []sourceTable, width int) string {
	if len(tables) == 0 {
		return rendered
	}
	tableByID := make(map[int]sourceTable, len(tables))
	for _, t := range tables {
		tableByID[t.id] = t
	}
	lines := strings.Split(strings.ReplaceAll(rendered, "\r\n", "\n"), "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		plain := utils.StripANSI(line)
		id := tableMarkerID(plain)
		if id >= 0 {
			if table, ok := tableByID[id]; ok {
				rendered := renderCompactTableSized(table, width)
				if table.blockquote {
					indent, leading := splitBlockquotePrefix(line)
					// If the marker shares a line with preceding blockquote text
					// (no blank line between them in the source), emit that text on
					// its own line before the table so it is not swallowed.
					if strings.TrimSpace(leading) != "" {
						out = append(out, indent+strings.TrimRight(leading, " "))
					}
					for _, l := range rendered {
						out = append(out, indent+l)
					}
				} else {
					out = append(out, rendered...)
				}
			}
			continue
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

func splitBlockquotePrefix(line string) (indent string, leading string) {
	plain := utils.StripANSI(line)
	// The glamour blockquote indent token is a box-drawing vertical line
	// followed by a space. When a blockquote table is preceded by other
	// blockquote text in the same paragraph, glamour merges the marker onto
	// that text's line, so the leading text must be recovered separately from
	// the indent that is repeated on each restored table row.
	if idx := strings.Index(plain, "│"); idx >= 0 {
		end := idx + len("│")
		if end < len(plain) && plain[end] == ' ' {
			end++
		}
		indent = plain[:end]
		markerPos := strings.Index(plain[end:], tableMarkerPrefix)
		if markerPos >= 0 {
			leading = plain[end : end+markerPos]
			// The marker is emitted as "<CODEX_INTERNAL_TABLE_N>"; drop the
			// opening "<" so the leading text does not retain a dangling marker.
			leading = strings.TrimSuffix(leading, "<")
		}
		return indent, leading
	}
	// Fallback for styles that do not use a box-drawing blockquote indent: the
	// content before the marker is the repeated prefix and there is no separate
	// leading text.
	idx := strings.Index(plain, tableMarkerPrefix)
	if idx < 0 {
		return "> ", ""
	}
	prefix := strings.TrimSuffix(plain[:idx], "<")
	if prefix == "" {
		prefix = "> "
	}
	return prefix, ""
}

func tableMarkerID(line string) int {
	idx := strings.Index(line, tableMarkerPrefix)
	if idx < 0 {
		return -1
	}
	rest := line[idx+len(tableMarkerPrefix):]
	end := strings.IndexByte(rest, '>')
	if end < 0 {
		return -1
	}
	id, err := strconv.Atoi(rest[:end])
	if err != nil {
		return -1
	}
	return id
}

// renderCompactTable renders a table in the Rust style: each column uses its
// natural content width (at least 3), rows are separated by a 2-space gutter,
// the header separator uses ━ and body rows are separated by ─.
func renderCompactTable(t sourceTable) []string {
	// Unconstrained rendering used by the table unit tests; the transcript path
	// goes through renderCompactTableSized so wide tables are kept in bounds.
	return renderCompactTableSized(t, 0)
}

func renderCompactTableSized(t sourceTable, maxWidth int) []string {
	colCount := len(t.header)
	if colCount == 0 {
		return nil
	}
	if maxWidth <= 0 {
		return renderCompactTableBlock(t, computeTableColumnWidths(t, colCount))
	}
	natural := computeTableColumnWidths(t, colCount)
	// The separator line is always one cell wider than the row layout, so the
	// overhead includes that extra column so neither rows nor separators exceed
	// the terminal width.
	overhead := tableRowOverhead(colCount) + 1
	if sumWidths(natural)+overhead <= maxWidth {
		return renderCompactTableBlock(t, natural)
	}
	budget := maxWidth - overhead
	if budget < colCount*minTableColumnWidth {
		// Even minimum-width columns would overflow; fall back to key/value
		// records so a multi-column table stays readable in a narrow terminal
		// (Rust parity: markdown_render table key/value fallback).
		return renderTableKeyValue(t, colCount, maxWidth)
	}
	widths := allocateColumnWidths(natural, budget, colCount)
	return renderCompactTableWideBlock(t, widths, colCount)
}

// renderCompactTableBlock renders a table at its natural (unconstrained) widths
// with a single-line row layout.
func renderCompactTableBlock(t sourceTable, widths []int) []string {
	colCount := len(t.header)
	header := normalizeTableRow(t.header, colCount)
	rows := make([][]string, len(t.rows))
	for i, row := range t.rows {
		rows[i] = normalizeTableRow(row, colCount)
	}
	out := make([]string, 0, 2+len(t.rows)*2)
	out = append(out, renderTableRow(header, widths, t.alignments))
	out = append(out, renderTableSeparator(widths, tableHeaderSeparatorChar))
	for i, row := range rows {
		out = append(out, renderTableRow(row, widths, t.alignments))
		if i+1 < len(t.rows) {
			out = append(out, renderTableSeparator(widths, tableBodySeparatorChar))
		}
	}
	return out
}

// renderCompactTableWideBlock renders a table when its natural width exceeds the
// terminal. Column widths are shrunk to fit and long cell content is wrapped so
// the table stays within the available width instead of overflowing.
func renderCompactTableWideBlock(t sourceTable, widths []int, colCount int) []string {
	header := normalizeTableRow(t.header, colCount)
	rows := make([][]string, len(t.rows))
	for i, row := range t.rows {
		rows[i] = normalizeTableRow(row, colCount)
	}
	out := make([]string, 0, 2+len(t.rows)*2)
	out = append(out, renderTableWideRowBlock(header, widths, t.alignments)...)
	out = append(out, renderTableSeparator(widths, tableHeaderSeparatorChar))
	for i, row := range rows {
		out = append(out, renderTableWideRowBlock(row, widths, t.alignments)...)
		if i+1 < len(t.rows) {
			out = append(out, renderTableSeparator(widths, tableBodySeparatorChar))
		}
	}
	return out
}

// renderTableWideRowBlock renders a single row where each cell may wrap to
// multiple display lines; the row block aligns the wrapped lines per column.
func renderTableWideRowBlock(cells []string, widths []int, alignments []string) []string {
	wrapped := make([][]string, len(cells))
	maxLines := 1
	for i, cell := range cells {
		if i >= len(widths) {
			break
		}
		content := stripInlineMarkdown(cell)
		lines := wrapCellLines(content, widths[i], tableAlignment(alignments, i))
		wrapped[i] = lines
		if len(lines) > maxLines {
			maxLines = len(lines)
		}
	}
	out := make([]string, 0, maxLines)
	for lineIndex := 0; lineIndex < maxLines; lineIndex++ {
		var sb strings.Builder
		for i := range wrapped {
			piece := ""
			if lineIndex < len(wrapped[i]) {
				piece = wrapped[i][lineIndex]
			}
			sb.WriteString(" ")
			sb.WriteString(piece)
			if i < len(wrapped)-1 {
				sb.WriteString(strings.Repeat(" ", tableCellPadding+tableColumnGap))
			}
		}
		out = append(out, strings.TrimRight(sb.String(), " "))
	}
	return out
}

// wrapCellLines wraps a cell's plain text to the column content width, padding
// each wrapped line to the aligned column width.
func wrapCellLines(content string, width int, align string) []string {
	if width <= 0 {
		return []string{""}
	}
	wrapped := codextui.AdaptiveWrapLine(content, codextui.WrapOptions{
		Width:      width,
		BreakWords: true,
	})
	if len(wrapped) == 0 {
		wrapped = []string{""}
	}
	out := make([]string, len(wrapped))
	for i, line := range wrapped {
		out[i] = alignCellLine(line, width, align)
	}
	return out
}

func alignCellLine(line string, width int, align string) string {
	remaining := width - codextui.DisplayWidth(line)
	if remaining < 0 {
		remaining = 0
	}
	switch align {
	case "center":
		left := remaining / 2
		return strings.Repeat(" ", left) + line + strings.Repeat(" ", remaining-left)
	case "right":
		return strings.Repeat(" ", remaining) + line
	default:
		return line + strings.Repeat(" ", remaining)
	}
}

// tableRowOverhead returns the fixed horizontal space consumed by the leading
// cell padding and the gutters between columns, excluding cell content widths.
func tableRowOverhead(colCount int) int {
	if colCount <= 0 {
		return 0
	}
	return colCount + (colCount-1)*(tableCellPadding+tableColumnGap)
}

func sumWidths(widths []int) int {
	total := 0
	for _, w := range widths {
		total += w
	}
	return total
}

// allocateColumnWidths shrinks the natural column widths so their sum fits the
// given content budget. It always removes width from the widest column first,
// so compact columns (short values, status labels) keep their natural width
// and remain scannable, mirroring Rust's column-classification priority.
func allocateColumnWidths(natural []int, budget int, colCount int) []int {
	widths := make([]int, colCount)
	copy(widths, natural)
	if sumWidths(widths) <= budget {
		return widths
	}
	toFree := sumWidths(widths) - budget
	for toFree > 0 {
		maxIdx := -1
		maxW := minTableColumnWidth
		secondMax := minTableColumnWidth
		for i, w := range widths {
			if w > maxW && (maxIdx < 0 || w > widths[maxIdx]) {
				maxIdx = i
				maxW = w
			}
		}
		if maxIdx < 0 {
			break
		}
		for i, w := range widths {
			if i != maxIdx && w > secondMax {
				secondMax = w
			}
		}
		dec := widths[maxIdx] - secondMax
		if dec <= 0 {
			dec = 1
		}
		if dec > widths[maxIdx]-minTableColumnWidth {
			dec = widths[maxIdx] - minTableColumnWidth
		}
		if dec > toFree {
			dec = toFree
		}
		if dec <= 0 {
			break
		}
		widths[maxIdx] -= dec
		toFree -= dec
	}
	return widths
}

func renderTableKeyValue(t sourceTable, colCount int, maxWidth int) []string {
	header := normalizeTableRow(t.header, colCount)
	// Precompute labels and the widest label so record colon separators align
	// across rows (Rust render_aligned_field).
	labels := make([]string, colCount)
	maxKey := 0
	for i := 0; i < colCount; i++ {
		label := stripInlineMarkdown(header[i])
		labels[i] = label
		if w := codextui.DisplayWidth(label); w > maxKey {
			maxKey = w
		}
	}
	lines := make([]string, 0)
	for _, row := range t.rows {
		if len(row) == 0 {
			continue
		}
		for i := 0; i < colCount && i < len(row); i++ {
			label := labels[i]
			value := stripInlineMarkdown(row[i])
			if label == "" {
				lines = append(lines, "\u2022 "+value)
				continue
			}
			labelText := label + ":"
			padded := labelText + strings.Repeat(" ", maxKey+1-codextui.DisplayWidth(labelText))
			prefix := "\u2022 " + padded
			avail := maxWidth - codextui.DisplayWidth(prefix)
			if avail < 1 {
				// Too narrow for an inline value: stack the value on the next line
				// (Rust render_stacked_field).
				lines = append(lines, "\u2022 "+labelText)
				for _, wl := range wrapValue(value, maxWidth-4) {
					lines = append(lines, "    "+wl)
				}
				continue
			}
			valueLines := wrapValue(value, avail)
			for k, wl := range valueLines {
				if k == 0 {
					lines = append(lines, prefix+" "+wl)
				} else {
					lines = append(lines, strings.Repeat(" ", codextui.DisplayWidth(prefix)+1)+wl)
				}
			}
		}
		lines = append(lines, "")
	}
	return lines
}

func wrapValue(value string, width int) []string {
	if width < 1 {
		return []string{value}
	}
	wrapped := codextui.AdaptiveWrapLine(value, codextui.WrapOptions{Width: width, BreakWords: true})
	if len(wrapped) == 0 {
		return []string{""}
	}
	return wrapped
}

func normalizeTableRow(row []string, colCount int) []string {
	if len(row) >= colCount {
		return row
	}
	normalized := make([]string, colCount)
	copy(normalized, row)
	return normalized
}

func computeTableColumnWidths(t sourceTable, colCount int) []int {
	widths := make([]int, colCount)
	for i := range widths {
		widths[i] = minTableColumnWidth
	}
	cells := make([][]string, 0, 1+len(t.rows))
	cells = append(cells, t.header)
	cells = append(cells, t.rows...)
	for _, row := range cells {
		for i, cell := range row {
			if i >= colCount {
				break
			}
			w := codextui.DisplayWidth(stripInlineMarkdown(cell))
			if w > widths[i] {
				widths[i] = w
			}
		}
	}
	return widths
}

func renderTableSeparator(widths []int, ch string) string {
	parts := make([]string, len(widths))
	for i, w := range widths {
		parts[i] = strings.Repeat(ch, w+2*tableCellPadding)
	}
	return strings.Join(parts, strings.Repeat(" ", tableColumnGap))
}

var (
	tableBoldRE   = regexp.MustCompile(`\*\*([^*]+)\*\*`)
	tableItalicRE = regexp.MustCompile(`\*([^*\s][^*]*)\*`)
	tableCodeRE   = regexp.MustCompile("`([^`]+)`")
)

// decorateTableInline applies ANSI styling to common inline markdown inside a
// table cell (bold, italic, code, links) so table cells are as rich as the
// surrounding transcript (Rust TableCell rich spans). Stripping ANSI restores
// the plain text used for column-width measurement.
func decorateTableInline(text string) string {
	if m := tableImageRE.FindStringSubmatch(text); m != nil {
		return decorateTableInline(m[1])
	}
	if m := tableLinkRE.FindStringSubmatch(text); m != nil {
		return "\x1b[36;4m" + decorateTableInline(m[1]) + "\x1b[0m"
	}
	text = tableBoldRE.ReplaceAllString(text, "\x1b[1m$1\x1b[0m")
	text = tableCodeRE.ReplaceAllString(text, "\x1b[36m$1\x1b[0m")
	text = tableItalicRE.ReplaceAllString(text, "\x1b[3m$1\x1b[0m")
	return text
}

func renderTableRow(cells []string, widths []int, alignments []string) string {
	var sb strings.Builder
	for i, cell := range cells {
		if i >= len(widths) {
			break
		}
		content := decorateTableInline(cell)
		contentWidth := codextui.DisplayWidth(utils.StripANSI(content))
		remaining := widths[i] - contentWidth
		if remaining < 0 {
			remaining = 0
		}
		var leftPad, rightPad int
		switch tableAlignment(alignments, i) {
		case "center":
			leftPad = remaining / 2
			rightPad = remaining - leftPad
		case "right":
			leftPad = remaining
		default:
			rightPad = remaining
		}
		isLast := i == len(cells)-1
		sb.WriteString(" ")
		if leftPad > 0 {
			sb.WriteString(strings.Repeat(" ", leftPad))
		}
		sb.WriteString(content)
		if rightPad > 0 && !isLast {
			sb.WriteString(strings.Repeat(" ", rightPad))
		}
		if !isLast {
			sb.WriteString(strings.Repeat(" ", tableCellPadding+tableColumnGap))
		}
	}
	return sb.String()
}

func tableAlignment(alignments []string, index int) string {
	if index < len(alignments) {
		return alignments[index]
	}
	return "none"
}

func stripInlineMarkdown(text string) string {
	if m := tableImageRE.FindStringSubmatch(text); m != nil {
		return stripInlineMarkdown(m[1])
	}
	if m := tableLinkRE.FindStringSubmatch(text); m != nil {
		return stripInlineMarkdown(m[1])
	}
	// Extract the plain text of a table cell with goldmark so inline markdown
	// (emphasis/code/strikethrough) is stripped the same way glamour strips it
	// for non-table content, while intraword underscores and asterisks (for
	// example snake_case identifiers) are preserved. Naive string replacement
	// of "*" and "_" would mangle snake_case cells into "snakecase".
	data := []byte(text)
	document := goldmark.New(goldmark.WithExtensions(extension.GFM)).Parser().Parse(gmtext.NewReader(data))
	var sb strings.Builder
	_ = ast.Walk(document, func(node ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch n := node.(type) {
		case *ast.Text:
			sb.Write(n.Segment.Value(data))
		case *ast.String:
			sb.Write(n.Value)
		}
		return ast.WalkContinue, nil
	})
	return strings.TrimSpace(sb.String())
}
