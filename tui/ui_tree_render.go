package tui

// Rust parity: codex-rs/tui/src/ui_tree/render.rs. Layout places prepared text
// and nested rows inside a bounded viewport, marks overflow with an unlinked
// ellipsis, and reports the measured size in terminal cells.

import (
	"sort"
	"strings"

	"github.com/rivo/uniseg"
)

type uiFragment struct {
	chunks    []uiChunk
	textWidth int
	gapWidth  int
	gapPaint  uiPaint
	isGap     bool
}

func (f uiFragment) width() int {
	if f.isGap {
		return f.gapWidth
	}
	return f.textWidth
}

func (f uiFragment) visit(limit int, draw func(offset int, text string, cells int, paint uiPaint)) int {
	if f.isGap {
		end := f.gapWidth
		if end > limit {
			end = limit
		}
		for start := 0; start < end; {
			count := end - start
			if count > 32 {
				count = 32
			}
			draw(start, strings.Repeat(" ", count), count, f.gapPaint)
			start += count
		}
		return end
	}
	return visitUIChunks(f.chunks, limit, draw)
}

func visitUIChunks(chunks []uiChunk, limit int, draw func(offset int, text string, cells int, paint uiPaint)) int {
	used := 0
	for index, chunk := range chunks {
		for _, run := range chunk.runs {
			text := run.text
			bytes := 0
			cells := 0
			graphemes := uniseg.NewGraphemes(text)
			for graphemes.Next() {
				width := DisplayWidth(graphemes.Str())
				if used+cells+width > limit {
					break
				}
				cells += width
				bytes += len(graphemes.Str())
			}
			if bytes > 0 && (used < limit || bytes == len(text)) {
				draw(used, text[:bytes], cells, run.paint)
			}
			used += cells
			if bytes < len(text) {
				return used
			}
		}
		if index+1 < len(chunks) && chunk.trailingSpace != nil {
			if used == limit {
				return used
			}
			draw(used, " ", 1, *chunk.trailingSpace)
			used++
		}
	}
	return used
}

func (t *preparedUIText) layout(width int, limit int, visit func(y int, fragment uiFragment)) int {
	y := 0
	for _, line := range t.lines {
		start := 0
		used := 0
		space := 0
		for index, chunk := range line {
			if index > start && used+space+chunk.widthCells > width {
				visit(y, uiFragment{chunks: line[start:index], textWidth: used})
				y++
				if y == limit {
					return y
				}
				start = index
				used = 0
				space = 0
			}
			used += space + chunk.widthCells
			if chunk.trailingSpace != nil {
				space = 1
			} else {
				space = 0
			}
		}
		visit(y, uiFragment{chunks: line[start:], textWidth: used})
		y++
		if y == limit {
			break
		}
	}
	return y
}

func layoutUINode(node *preparedUINode, width int, limit int, visit func(y int, fragment uiFragment)) int {
	if node == nil || width <= 0 || limit <= 0 {
		return 0
	}
	if node.text != nil {
		return node.text.layout(width, limit, visit)
	}
	row := node.row
	if row == nil {
		return 0
	}
	gap := width + 1
	if row.gap < uint64(gap) {
		gap = int(row.gap)
	}
	y := 0
	start := 0
	used := 0
	for index, child := range row.children {
		preferred := int(child.widths.preferred)
		if preferred > width+1 {
			preferred = width + 1
		}
		if row.wrap == UIRowWrapWrap && index > start && used+gap+preferred > width {
			y += layoutUIBand(row.children[start:index], width, limit-y, gap, row.paint, func(rowOffset int, fragment uiFragment) {
				visit(y+rowOffset, fragment)
			})
			if y == limit {
				return y
			}
			start = index
			used = 0
		}
		if index > start {
			used += gap
		}
		used += preferred
	}
	y += layoutUIBand(row.children[start:], width, limit-y, gap, row.paint, func(rowOffset int, fragment uiFragment) {
		visit(y+rowOffset, fragment)
	})
	return y
}

func layoutUIBand(children []*preparedUINode, width int, limit int, gap int, paint uiPaint, visit func(y int, fragment uiFragment)) int {
	if limit <= 0 {
		return 0
	}
	bounded := func(cells uint64) int {
		if cells > uint64(width+1) {
			return width + 1
		}
		return int(cells)
	}
	reserved := gap * max(len(children)-1, 0)
	for _, child := range children {
		reserved += bounded(child.widths.minimum)
	}
	offset := 0
	ends := make([]int, 0, 4)
	for _, child := range children {
		minimum := bounded(child.widths.minimum)
		reserved -= minimum
		available := bounded(child.widths.preferred)
		room := width - (offset + reserved)
		if room < 0 {
			room = 0
		}
		if available > room {
			available = room
		}
		if available < minimum {
			available = minimum
		}
		if available < 1 {
			available = 1
		}
		if available > width {
			available = width
		}
		layoutUINode(child, available, limit, func(y int, part uiFragment) {
			for len(ends) <= y {
				ends = append(ends, 0)
			}
			start := offset
			if start > width+1 {
				start = width + 1
			}
			if start > ends[y] {
				visit(y, uiFragment{gapWidth: start - ends[y], gapPaint: paint, isGap: true})
				ends[y] = start
			}
			if offset <= width {
				visit(y, part)
				ends[y] += part.width()
			}
		})
		offset += available + gap
		reserved -= gap
		if reserved < 0 {
			reserved = 0
		}
	}
	return len(ends)
}

type uiDrawCall struct {
	column int
	text   string
	cells  int
	paint  uiPaint
}

type uiRowState struct {
	width     int
	hasEdge   bool
	edgeStart int
	edge      uiFragment
	lastPaint *uiPaint
}

func layoutUITree(root *preparedUINode, width int, height int, draw func(row int, column int, text string, cells int, paint uiPaint)) (int, int) {
	if root == nil || width <= 0 || height <= 0 {
		return 0, 0
	}
	rows := make([]*uiRowState, 0, 4)
	total := layoutUINode(root, width, height+1, func(y int, fragment uiFragment) {
		if y >= height {
			return
		}
		for len(rows) <= y {
			rows = append(rows, nil)
		}
		if rows[y] == nil {
			rows[y] = &uiRowState{}
		}
		row := rows[y]
		start := row.width
		row.width += fragment.width()
		if start >= width {
			return
		}
		if row.width >= width {
			row.hasEdge = true
			row.edgeStart = start
			row.edge = fragment
			return
		}
		if draw == nil {
			return
		}
		fragment.visit(1<<30, func(offset int, text string, cells int, paint uiPaint) {
			draw(y, start+offset, text, cells, paint)
			copied := paint
			row.lastPaint = &copied
		})
	})
	measuredWidth := 0
	measuredHeight := total
	if measuredHeight > height {
		measuredHeight = height
	}
	for y := 0; y < measuredHeight && y < len(rows); y++ {
		row := rows[y]
		if row == nil {
			continue
		}
		clipped := row.width > width || (y+1 == height && total > height)
		end := row.width
		var lastPaint *uiPaint
		if row.lastPaint != nil {
			copied := *row.lastPaint
			lastPaint = &copied
		}
		switch {
		case clipped && width == 1:
			end = 0
			lastPaint = nil
		case row.hasEdge && (clipped || draw != nil):
			limit := width - boolToInt(clipped) - row.edgeStart
			if limit < 0 {
				limit = 0
			}
			end = row.edgeStart + row.edge.visit(limit, func(offset int, text string, cells int, paint uiPaint) {
				if draw != nil {
					draw(y, row.edgeStart+offset, text, cells, paint)
				}
				copied := paint
				lastPaint = &copied
			})
		}
		if clipped {
			paint := uiPaint{}
			if lastPaint != nil {
				paint = *lastPaint
			}
			paint.href = ""
			if draw != nil {
				draw(y, end, "…", 1, paint)
			}
			end++
		}
		if end > measuredWidth {
			measuredWidth = end
		}
	}
	return measuredWidth, measuredHeight
}

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

// Measure reports the display size of the tree inside the supplied bounds.
func (t PreparedUITree) Measure(width int, height int) (int, int) {
	return layoutUITree(t.root, width, height, nil)
}

// RenderLines lays the tree out inside the viewport and returns one display row
// per measured line, each carrying styled spans and hyperlink ranges.
func (t PreparedUITree) RenderLines(width int, height int) []UILine {
	if t.root == nil || width <= 0 || height <= 0 {
		return nil
	}
	calls := make([][]uiDrawCall, height)
	_, measuredHeight := layoutUITree(t.root, width, height, func(row int, column int, text string, cells int, paint uiPaint) {
		if row < 0 || row >= height || text == "" {
			return
		}
		calls[row] = append(calls[row], uiDrawCall{column: column, text: text, cells: cells, paint: paint})
	})
	lines := make([]UILine, measuredHeight)
	for row := 0; row < measuredHeight; row++ {
		lines[row] = uiLineFromDrawCalls(calls[row])
	}
	return lines
}

func uiLineFromDrawCalls(calls []uiDrawCall) UILine {
	if len(calls) == 0 {
		return UILine{}
	}
	sort.SliceStable(calls, func(i, j int) bool { return calls[i].column < calls[j].column })
	var line UILine
	var text strings.Builder
	column := 0
	appendSpan := func(value string, style UIStyle) {
		if value == "" {
			return
		}
		if count := len(line.Spans); count > 0 && line.Spans[count-1].Style == style {
			line.Spans[count-1].Text += value
			return
		}
		line.Spans = append(line.Spans, UISpan{Text: value, Style: style})
	}
	for _, call := range calls {
		if call.column < column {
			continue
		}
		if call.column > column {
			padding := strings.Repeat(" ", call.column-column)
			text.WriteString(padding)
			appendSpan(padding, UIStyle{})
		}
		text.WriteString(call.text)
		appendSpan(call.text, call.paint.style())
		if call.paint.href != "" && call.cells > 0 {
			line.Hyperlinks = append(line.Hyperlinks, TerminalHyperlink{
				Start:       call.column,
				End:         call.column + call.cells,
				Destination: call.paint.href,
			})
		}
		column = call.column + call.cells
	}
	line.Text = text.String()
	return line
}

type uiCell struct {
	text  string
	style UIStyle
	href  string
}

func (l UILine) buildCells() []uiCell {
	spans := l.Spans
	if len(spans) == 0 {
		if l.Text == "" {
			return nil
		}
		spans = []UISpan{{Text: l.Text}}
	}
	cells := make([]uiCell, 0, len(l.Text))
	column := 0
	for _, span := range spans {
		graphemes := uniseg.NewGraphemes(span.Text)
		for graphemes.Next() {
			value := graphemes.Str()
			width := DisplayWidth(value)
			href := uiHyperlinkAt(l.Hyperlinks, column)
			if width == 0 && len(cells) > 0 {
				cells[len(cells)-1].text += value
				continue
			}
			cells = append(cells, uiCell{text: value, style: span.Style, href: href})
			column += width
		}
	}
	return cells
}

func uiHyperlinkAt(links []TerminalHyperlink, column int) string {
	for _, link := range links {
		if column >= link.Start && column < link.End {
			return link.Destination
		}
	}
	return ""
}

// RenderStyled emits the row with its styled spans and OSC-8 hyperlinks.
// Callers that only need plain text keep using Render.
func (l UILine) RenderStyled(themeID string, level StdoutColorLevel) string {
	if len(l.Spans) == 0 && len(l.Hyperlinks) == 0 {
		return l.Text
	}
	cells := l.buildCells()
	if len(cells) == 0 {
		return ""
	}
	var out strings.Builder
	var activeStyle UIStyle
	styleOpen := false
	activeHref := ""
	for _, cell := range cells {
		if cell.style != activeStyle {
			if styleOpen {
				out.WriteString("\x1b[0m")
				styleOpen = false
			}
			if sgr := uiStyleSGR(cell.style, themeID, level); sgr != "" {
				out.WriteString(sgr)
				styleOpen = true
			}
			activeStyle = cell.style
		}
		if cell.href != activeHref {
			if activeHref != "" {
				out.WriteString(osc8CloseSequence)
				activeHref = ""
			}
			if open := osc8OpenSequence(cell.href); open != "" {
				out.WriteString(open)
				activeHref = cell.href
			}
		}
		out.WriteString(cell.text)
	}
	if activeHref != "" {
		out.WriteString(osc8CloseSequence)
	}
	if styleOpen {
		out.WriteString("\x1b[0m")
	}
	return out.String()
}
