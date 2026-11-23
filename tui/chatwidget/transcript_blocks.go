package chatwidget

import (
	"sort"
	"strings"
)

// Rust parity: codex-rs/tui/src/transcript_view/select_mode/blocks.rs and
// markdown_copy/block.rs (#63047). Rust renders Markdown while recording each
// block's ancestry; this pager keeps rendered plain rows, so it rebuilds the
// same structure (headings, paragraphs, lists, quotes and tables) from those
// rows and converts a marked range back to Markdown for copying.

// markdownBlockKind mirrors markdown_copy::block::Kind.
type markdownBlockKind int

const (
	markdownBlockParagraph markdownBlockKind = iota
	markdownBlockHeading
	markdownBlockCode
	markdownBlockQuote
	markdownBlockList
	markdownBlockItem
	markdownBlockTable
)

// label matches the Rust `Kind::label` strings shown in the selection footer.
func (k markdownBlockKind) label() string {
	switch k {
	case markdownBlockHeading:
		return "heading"
	case markdownBlockCode:
		return "code block"
	case markdownBlockQuote:
		return "blockquote"
	case markdownBlockList:
		return "list"
	case markdownBlockItem:
		return "list item"
	case markdownBlockTable:
		return "table"
	default:
		return "paragraph"
	}
}

// markdownBlock indexes one Markdown block over rendered content rows.
// Heading is set only on section blocks, mirroring Rust's BlockRange: a section
// wraps a heading and everything it owns, and expand_to prefers it over Parent.
type markdownBlock struct {
	Kind    markdownBlockKind
	Level   int
	Parent  int
	Section int
	Heading int
	Start   int
	End     int
	Table   int
}

const noBlock = -1

func (b markdownBlock) section() bool { return b.Heading != noBlock }

// blockListMarker is one rendered list marker ("- ", "1. ").
type blockListMarker struct {
	indent  int
	ordered bool
	auth    string
}

// splitTranscriptPrefix removes the transcript cell prefix that every rendered
// entry carries: the first assistant row starts with a bullet, later rows are
// indented by two columns. The rest is the Markdown row itself.
func splitTranscriptPrefix(line string) (string, string) {
	if strings.HasPrefix(line, "\u2022 ") {
		return "\u2022 ", line[len("\u2022 "):]
	}
	if strings.HasPrefix(line, "  ") {
		return "  ", line[2:]
	}
	return "", line
}

// parseBlockListMarker recognizes a rendered list item marker at the start of a
// content row.
func parseBlockListMarker(content string) (blockListMarker, bool) {
	runes := []rune(content)
	index := 0
	for index < len(runes) && runes[index] == ' ' {
		index++
	}
	if index >= len(runes) {
		return blockListMarker{}, false
	}
	start := index
	switch runes[index] {
	case '\u2022', '-', '*', '+':
		if index+1 < len(runes) && runes[index+1] == ' ' {
			return blockListMarker{indent: start, auth: string(runes[index])}, true
		}
		return blockListMarker{}, false
	}
	for index < len(runes) && runes[index] >= '0' && runes[index] <= '9' {
		index++
	}
	if index == start || index >= len(runes) {
		return blockListMarker{}, false
	}
	if (runes[index] != '.' && runes[index] != ')') || index+1 >= len(runes) || runes[index+1] != ' ' {
		return blockListMarker{}, false
	}
	return blockListMarker{indent: start, ordered: true, auth: string(runes[start : index+1])}, true
}

// quoteDepth counts leading blockquote bars ("\u2502 text" renders one level,
// "\u2502 \u2502 text" two).
func quoteDepth(content string) int {
	runes := []rune(content)
	index := 0
	depth := 0
	for index < len(runes) {
		next := index
		for next < len(runes) && runes[next] == ' ' {
			next++
		}
		if next < len(runes) && runes[next] == '\u2502' {
			depth++
			index = next + 1
			if index < len(runes) && runes[index] == ' ' {
				index++
			}
			continue
		}
		break
	}
	return depth
}

// headingMarkerLevel reports the rendered "# " prefix of a heading row.
func headingMarkerLevel(content string) int {
	count := 0
	for count < len(content) && content[count] == '#' {
		count++
	}
	if count == 0 || count > 6 {
		return 0
	}
	if count < len(content) && content[count] == ' ' {
		return count
	}
	return 0
}

// fenceMarker reports the fence token of a code block row. Completed
// transcripts render fenced code without markers, but the raw-output pager
// keeps them, so the same block model serves both surfaces.
func fenceMarker(content string) (string, bool) {
	trimmed := strings.TrimSpace(content)
	for _, token := range []string{"```", "~~~"} {
		if strings.HasPrefix(trimmed, token) {
			return token, true
		}
	}
	return "", false
}

// buildMarkdownBlocks recovers the Markdown block tree of each document from
// rendered rows. Document boundaries come from the transcript message ranges so
// a heading section never swallows the next message.
func buildMarkdownBlocks(lines []string, tables []tableLayout, documents [][2]int) []markdownBlock {
	parser := &markdownBlockParser{lines: lines, tables: tables}
	parser.tableAt = make(map[int]int, len(tables)*4)
	for index, table := range tables {
		if len(table.Rows) == 0 {
			continue
		}
		start := table.Rows[0].Line
		end := table.Rows[len(table.Rows)-1].Line
		for row := start; row <= end && row < len(lines); row++ {
			parser.tableAt[row] = index
		}
	}
	if len(documents) == 0 {
		documents = [][2]int{{0, len(lines)}}
	}
	for _, document := range documents {
		from, to := document[0], document[1]
		if from < 0 {
			from = 0
		}
		if to > len(lines) {
			to = len(lines)
		}
		if from >= to {
			continue
		}
		first := len(parser.blocks)
		parser.parseRange(from, to, noBlock)
		parser.addSections(first, from, to)
	}
	return parser.blocks
}

type markdownBlockParser struct {
	lines   []string
	tables  []tableLayout
	blocks  []markdownBlock
	tableAt map[int]int
}

func (p *markdownBlockParser) add(block markdownBlock) int {
	block.Section = noBlock
	block.Heading = noBlock
	p.blocks = append(p.blocks, block)
	return len(p.blocks) - 1
}

func (p *markdownBlockParser) addWith(block markdownBlock) int {
	p.blocks = append(p.blocks, block)
	return len(p.blocks) - 1
}

// startsBlock reports whether a content row opens a new Markdown block, which
// ends the paragraph or item body above it.
func (p *markdownBlockParser) startsBlock(row int) bool {
	if _, ok := p.tableAt[row]; ok {
		return true
	}
	_, content := splitTranscriptPrefix(p.lines[row])
	if quoteDepth(content) > 0 {
		return true
	}
	if _, ok := parseBlockListMarker(content); ok {
		return true
	}
	if headingMarkerLevel(content) > 0 {
		return true
	}
	if _, ok := fenceMarker(content); ok {
		return true
	}
	return false
}

func (p *markdownBlockParser) parseRange(from int, to int, parent int) int {
	row := from
	for row < to {
		if strings.TrimSpace(p.lines[row]) == "" {
			row++
			continue
		}
		if table, ok := p.tableAt[row]; ok {
			tableRows := p.tables[table].Rows
			start := tableRows[0].Line
			end := tableRows[len(tableRows)-1].Line
			if end >= to {
				end = to - 1
			}
			p.add(markdownBlock{Kind: markdownBlockTable, Parent: parent, Start: start, End: end, Table: table})
			row = end + 1
			continue
		}
		_, content := splitTranscriptPrefix(p.lines[row])
		if depth := quoteDepth(content); depth > 0 {
			row, _ = p.parseQuote(row, to, parent, depth)
			continue
		}
		if marker, ok := parseBlockListMarker(content); ok {
			row, _ = p.parseList(row, to, parent, marker)
			continue
		}
		if level := headingMarkerLevel(content); level > 0 {
			row = p.parseHeading(row, to, parent, level)
			continue
		}
		if fence, ok := fenceMarker(content); ok {
			row = p.parseFence(row, to, parent, fence)
			continue
		}
		row = p.parseParagraph(row, to, parent)
	}
	return to
}

func (p *markdownBlockParser) parseParagraph(from int, to int, parent int) int {
	row := from + 1
	for row < to {
		if strings.TrimSpace(p.lines[row]) == "" || p.startsBlock(row) {
			break
		}
		row++
	}
	p.add(markdownBlock{Kind: markdownBlockParagraph, Parent: parent, Start: from, End: row - 1})
	return row
}

func (p *markdownBlockParser) parseHeading(from int, to int, parent int, level int) int {
	row := from + 1
	for row < to {
		if strings.TrimSpace(p.lines[row]) == "" || p.startsBlock(row) {
			break
		}
		row++
	}
	p.add(markdownBlock{Kind: markdownBlockHeading, Level: level, Parent: parent, Start: from, End: row - 1})
	return row
}

func (p *markdownBlockParser) parseFence(from int, to int, parent int, fence string) int {
	row := from + 1
	for row < to {
		_, content := splitTranscriptPrefix(p.lines[row])
		if strings.HasPrefix(strings.TrimSpace(content), fence) {
			row++
			break
		}
		row++
	}
	p.add(markdownBlock{Kind: markdownBlockCode, Parent: parent, Start: from, End: row - 1})
	return row
}

func (p *markdownBlockParser) listMarkerAt(row int) (blockListMarker, bool) {
	if row < 0 || row >= len(p.lines) {
		return blockListMarker{}, false
	}
	_, content := splitTranscriptPrefix(p.lines[row])
	if quoteDepth(content) > 0 {
		return blockListMarker{}, false
	}
	return parseBlockListMarker(content)
}

func (p *markdownBlockParser) parseList(from int, to int, parent int, first blockListMarker) (int, int) {
	list := p.add(markdownBlock{Kind: markdownBlockList, Level: first.indent, Parent: parent, Start: from, End: from})
	row := from
	for row < to {
		marker, ok := p.listMarkerAt(row)
		if !ok || marker.indent != first.indent || marker.ordered != first.ordered {
			break
		}
		item := p.add(markdownBlock{Kind: markdownBlockItem, Level: marker.indent, Parent: list, Start: row, End: row})
		body := row + 1
		for body < to {
			if strings.TrimSpace(p.lines[body]) == "" || p.startsBlock(body) {
				break
			}
			body++
		}
		p.blocks[item].End = body - 1
		// Nested lists belong to the item that contains them (Rust Item -> List).
		for body < to {
			nested, ok := p.listMarkerAt(body)
			if !ok || nested.indent <= marker.indent {
				break
			}
			next, child := p.parseList(body, to, item, nested)
			if next <= body {
				break
			}
			if p.blocks[child].End > p.blocks[item].End {
				p.blocks[item].End = p.blocks[child].End
			}
			body = next
		}
		if p.blocks[item].End > p.blocks[list].End {
			p.blocks[list].End = p.blocks[item].End
		}
		next := body
		for next < to && strings.TrimSpace(p.lines[next]) == "" {
			next++
		}
		if next < to {
			if sibling, ok := p.listMarkerAt(next); ok && sibling.indent == first.indent && sibling.ordered == first.ordered {
				row = next
				continue
			}
		}
		return next, list
	}
	return row, list
}

func (p *markdownBlockParser) parseQuote(from int, to int, parent int, depth int) (int, int) {
	quote := p.add(markdownBlock{Kind: markdownBlockQuote, Level: depth, Parent: parent, Start: from, End: from})
	row := from
	for row < to {
		if strings.TrimSpace(p.lines[row]) == "" {
			break
		}
		_, content := splitTranscriptPrefix(p.lines[row])
		current := quoteDepth(content)
		if current < depth {
			break
		}
		if current > depth {
			nestedStart := row
			for row < to {
				_, nestedContent := splitTranscriptPrefix(p.lines[row])
				if quoteDepth(nestedContent) <= depth {
					break
				}
				row++
			}
			p.add(markdownBlock{Kind: markdownBlockQuote, Level: depth + 1, Parent: quote, Start: nestedStart, End: row - 1})
			p.blocks[quote].End = row - 1
			continue
		}
		p.blocks[quote].End = row
		row++
	}
	return row, quote
}

// addSections appends Rust's heading-aware section blocks for one document. A
// section starts at its heading and ends before the next heading of the same or
// a higher rank.
func (p *markdownBlockParser) addSections(first int, from int, to int) {
	parsed := len(p.blocks)
	open := make([]int, 0, 4)
	for id := first; id < parsed; id++ {
		block := p.blocks[id]
		if block.Kind == markdownBlockHeading {
			for len(open) > 0 && p.blocks[open[len(open)-1]].Level >= block.Level {
				section := open[len(open)-1]
				end := block.Start - 1
				for end > p.blocks[section].Start && end >= from && strings.TrimSpace(p.lines[end]) == "" {
					end--
				}
				if end > p.blocks[section].End {
					p.blocks[section].End = end
				}
				open = open[:len(open)-1]
			}
			parent := block.Parent
			if len(open) > 0 {
				parent = open[len(open)-1]
			}
			section := p.addWith(markdownBlock{
				Kind:    markdownBlockHeading,
				Level:   block.Level,
				Parent:  parent,
				Section: noBlock,
				Heading: id,
				Start:   block.Start,
				End:     block.End,
				Table:   noBlock,
			})
			open = append(open, section)
		}
		if len(open) > 0 && p.blocks[id].Section == noBlock {
			p.blocks[id].Section = open[len(open)-1]
		}
		for _, section := range open {
			if p.blocks[id].End > p.blocks[section].End {
				p.blocks[section].End = p.blocks[id].End
			}
		}
	}
	for _, section := range open {
		if p.blocks[section].End > to-1 {
			p.blocks[section].End = to - 1
		}
	}
}

// markdownBlockAt returns the innermost Markdown block under a content row.
func (o *TranscriptOverlay) markdownBlockAt(row int) (int, bool) {
	if o == nil || row < 0 {
		return noBlock, false
	}
	best := noBlock
	bestSpan := 0
	for id, block := range o.blocks {
		if block.section() || row < block.Start || row > block.End {
			continue
		}
		span := block.End - block.Start
		if best == noBlock || span <= bestSpan {
			best = id
			bestSpan = span
		}
	}
	return best, best != noBlock
}

func (o *TranscriptOverlay) markdownBlockRows(id int) (int, int, bool) {
	if o == nil || id < 0 || id >= len(o.blocks) {
		return 0, 0, false
	}
	block := o.blocks[id]
	if block.Start < 0 || block.End < block.Start || block.Start >= len(o.plainLines) {
		return 0, 0, false
	}
	return block.Start, block.End, true
}

func (o *TranscriptOverlay) markdownBlockLabel(id int) string {
	if o == nil || id < 0 || id >= len(o.blocks) {
		return "block"
	}
	block := o.blocks[id]
	if block.section() {
		return "section"
	}
	return block.Kind.label()
}

func (o *TranscriptOverlay) markdownBlockExpandTo(id int) int {
	if o == nil || id < 0 || id >= len(o.blocks) {
		return noBlock
	}
	block := o.blocks[id]
	if block.Section != noBlock {
		return block.Section
	}
	return block.Parent
}

// markdownBlockSiblings lists the blocks sharing a parent, in document order,
// with the same section rank (Rust blocks::move_block).
func (o *TranscriptOverlay) markdownBlockSiblings(id int) []int {
	if o == nil || id < 0 || id >= len(o.blocks) {
		return nil
	}
	parent := o.blocks[id].Parent
	section := o.blocks[id].section()
	siblings := make([]int, 0, 4)
	for other, block := range o.blocks {
		if block.Parent == parent && block.section() == section {
			siblings = append(siblings, other)
		}
	}
	return siblings
}

func (o *TranscriptOverlay) markdownBlockTarget(anchor int, active int) (int, bool) {
	if o == nil || anchor < 0 || active < 0 || anchor >= len(o.blocks) || active >= len(o.blocks) {
		return noBlock, false
	}
	activeExpand := o.markdownBlockExpandTo(active)
	anchorExpand := o.markdownBlockExpandTo(anchor)
	if activeExpand == anchorExpand {
		if activeExpand == noBlock {
			return noBlock, false
		}
		return activeExpand, true
	}
	if parent := o.blocks[active].Parent; parent != noBlock && parent == o.blocks[anchor].Parent {
		return parent, true
	}
	return noBlock, false
}

// markdownBlockText renders a marked block range back to Markdown, restoring
// heading markers, list markers, quote bars and table formatting that the
// rendered rows carry implicitly.
func (o *TranscriptOverlay) markdownBlockText() string {
	start, end, ok := o.markedBlockRows()
	if !ok {
		return ""
	}
	lines := make([]string, 0, end-start+1)
	for row := start; row <= end && row < len(o.plainLines); {
		if id, found := o.markdownTableBlockAt(row); found {
			block := o.blocks[id]
			if block.Start >= start && block.End <= end {
				lines = append(lines, o.markdownTableText(block.Table, 0, -1, 0, -1))
				row = block.End + 1
				continue
			}
		}
		lines = append(lines, o.markdownRowText(row))
		row++
	}
	for len(lines) > 0 && strings.TrimSpace(lines[0]) == "" {
		lines = lines[1:]
	}
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	return strings.Join(lines, "\n")
}

func (o *TranscriptOverlay) markedBlockRows() (int, int, bool) {
	anchorStart, anchorEnd, ok := o.markdownBlockRows(o.selectMode.anchorBlock)
	if !ok {
		return 0, 0, false
	}
	activeStart, activeEnd, ok := o.markdownBlockRows(o.selectMode.activeBlock)
	if !ok {
		return 0, 0, false
	}
	start, end := anchorStart, anchorEnd
	if activeStart < start {
		start = activeStart
	}
	if activeEnd > end {
		end = activeEnd
	}
	return start, end, true
}

func (o *TranscriptOverlay) markdownTableBlockAt(row int) (int, bool) {
	for id, block := range o.blocks {
		if block.section() || block.Kind != markdownBlockTable {
			continue
		}
		if row >= block.Start && row <= block.End {
			return id, true
		}
	}
	return noBlock, false
}

func (o *TranscriptOverlay) markdownRowText(row int) string {
	if o == nil || row < 0 || row >= len(o.plainLines) {
		return ""
	}
	_, content := splitTranscriptPrefix(o.plainLines[row])
	content = strings.TrimRight(content, " ")
	if strings.TrimSpace(content) == "" {
		return ""
	}
	if depth := quoteDepth(content); depth > 0 {
		content = quoteMarkdown(content, depth)
	}
	if marker, ok := parseBlockListMarker(content); ok && !marker.ordered {
		content = strings.Replace(content, marker.auth, "-", 1)
	}
	return strings.TrimRight(content, " ")
}

// quoteMarkdown rewrites the rendered quote bars into Markdown markers.
func quoteMarkdown(content string, depth int) string {
	runes := []rune(content)
	index := 0
	for level := 0; level < depth && index < len(runes); level++ {
		for index < len(runes) && runes[index] == ' ' {
			index++
		}
		if index < len(runes) && runes[index] == '\u2502' {
			runes[index] = '>'
			index++
			if index < len(runes) && runes[index] == ' ' {
				continue
			}
		}
	}
	return string(runes)
}

// markdownTableText rebuilds a Markdown table. minRow < 0 selects every row and
// minCol < 0 every column.
func (o *TranscriptOverlay) markdownTableText(tableIndex int, minRow int, maxRow int, minCol int, maxCol int) string {
	if o == nil || tableIndex < 0 || tableIndex >= len(o.tables) {
		return ""
	}
	table := o.tables[tableIndex]
	columns := table.columns()
	if columns == 0 || len(table.Rows) == 0 {
		return ""
	}
	if minRow < 0 {
		minRow = 0
	}
	if maxRow < 0 || maxRow >= len(table.Rows) {
		maxRow = len(table.Rows) - 1
	}
	if minCol < 0 {
		minCol = 0
	}
	if maxCol < 0 || maxCol >= columns {
		maxCol = columns - 1
	}
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

// blockMarkState remembers one expansion level so Shift+Tab can restore the
// range that Tab expanded from (Rust BlockMark::section_origin/table).
type blockMarkState struct {
	table       bool
	tableAnchor tableCellPosition
	tableActive tableCellPosition
	anchor      int
	active      int
}

// toggleMarkdown is Rust toggle_markdown: a second `m` clears the block mark;
// a caret inside a table marks the cell rectangle first, and any other caret
// marks the innermost Markdown block.
func (o *TranscriptOverlay) toggleMarkdown() bool {
	if o == nil || !o.selectMode.active {
		return false
	}
	if o.selectMode.blockMark {
		cursor := o.selectCursor()
		o.selectMode.blockMark = false
		o.selectMode.anchorBlock = noBlock
		o.selectMode.activeBlock = noBlock
		o.selectMode.blockBack = nil
		o.selectMode.marked = false
		o.selectMode.unit = SelectUnitCharacter
		o.selectMode.anchor = cursor
		o.selectMode.activeEnd = cursor
		return true
	}
	if o.startSelectTable() {
		return true
	}
	id, ok := o.markdownBlockAt(o.selectCursor().Row)
	if !ok {
		return false
	}
	o.installBlockMark(id, id)
	return true
}

func (o *TranscriptOverlay) installBlockMark(anchor int, active int) {
	o.selectMode.marked = true
	o.selectMode.unit = SelectUnitBlock
	o.selectMode.blockMark = true
	o.selectMode.tableMark = false
	o.selectMode.tableText = false
	o.selectMode.anchorCell = tableCellPosition{}
	o.selectMode.activeCell = tableCellPosition{}
	o.selectMode.anchorBlock = anchor
	o.selectMode.activeBlock = active
	o.selectMode.anchor = o.blockStartPoint(anchor)
	o.selectMode.activeEnd = o.blockStartPoint(active)
	o.selectMode.preferredColumn = -1
	o.selectMode.searchOrigin = nil
}

func (o *TranscriptOverlay) blockStartPoint(id int) selectPoint {
	if start, _, ok := o.markdownBlockRows(id); ok {
		return selectPoint{Row: start}
	}
	return o.selectCursor()
}

func (o *TranscriptOverlay) extendBlockMark(active int) {
	o.selectMode.marked = true
	o.selectMode.unit = SelectUnitBlock
	o.selectMode.blockMark = true
	o.selectMode.activeBlock = active
	o.selectMode.activeEnd = o.blockStartPoint(active)
}

// handleBlockMarkCommand applies one motion while a block is marked. Sibling
// motion replaces the mark (h/l) or extends it (j/k); other motions are
// consumed without leaving the block scope (Rust move_block).
func (o *TranscriptOverlay) handleBlockMarkCommand(command SelectCommand) SelectAction {
	siblings := o.markdownBlockSiblings(o.selectMode.activeBlock)
	if len(siblings) == 0 {
		return SelectActionNone
	}
	position := -1
	for index, id := range siblings {
		if id == o.selectMode.activeBlock {
			position = index
			break
		}
	}
	if position < 0 {
		return SelectActionNone
	}
	target := noBlock
	collapse := true
	switch command {
	case SelectCmdLeft:
		if position > 0 {
			target = siblings[position-1]
		}
	case SelectCmdRight:
		if position+1 < len(siblings) {
			target = siblings[position+1]
		}
	case SelectCmdUp:
		if position > 0 {
			target = siblings[position-1]
			collapse = false
		}
	case SelectCmdDown:
		if position+1 < len(siblings) {
			target = siblings[position+1]
			collapse = false
		}
	case SelectCmdLineStart, SelectCmdBeginning:
		target = siblings[0]
	case SelectCmdLineEnd, SelectCmdEnd:
		target = siblings[len(siblings)-1]
	default:
		return SelectActionNone
	}
	if target == noBlock || target == o.selectMode.activeBlock {
		return SelectActionNone
	}
	if collapse {
		o.installBlockMark(target, target)
	} else {
		o.extendBlockMark(target)
	}
	o.revealSelectionCursor()
	o.viewport.SetContent(o.viewContent())
	return SelectActionChanged
}

func (o *TranscriptOverlay) markdownBlockForTable(index int) int {
	for id, block := range o.blocks {
		if !block.section() && block.Kind == markdownBlockTable && block.Table == index {
			return id
		}
	}
	return noBlock
}

// expandBlockMark implements Tab: a cell rectangle grows to its table block and
// a block grows to the enclosing block or heading section.
func (o *TranscriptOverlay) expandBlockMark() bool {
	if o == nil || !o.selectMode.active {
		return false
	}
	if !o.selectMode.blockMark {
		if !o.selectMode.tableMark {
			return false
		}
		id := o.markdownBlockForTable(o.selectMode.anchorCell.Table)
		if id == noBlock {
			return false
		}
		o.selectMode.blockBack = append(o.selectMode.blockBack, blockMarkState{
			table:       true,
			tableAnchor: o.selectMode.anchorCell,
			tableActive: o.selectMode.activeCell,
		})
		o.installBlockMark(id, id)
		return true
	}
	target, ok := o.markdownBlockTarget(o.selectMode.anchorBlock, o.selectMode.activeBlock)
	if !ok {
		return false
	}
	o.selectMode.blockBack = append(o.selectMode.blockBack, blockMarkState{
		anchor: o.selectMode.anchorBlock,
		active: o.selectMode.activeBlock,
	})
	o.installBlockMark(target, target)
	return true
}

// shrinkBlockMark implements Shift+Tab by restoring the range Tab expanded.
func (o *TranscriptOverlay) shrinkBlockMark() bool {
	if o == nil || !o.selectMode.active || !o.selectMode.blockMark {
		return false
	}
	if len(o.selectMode.blockBack) == 0 {
		return false
	}
	state := o.selectMode.blockBack[len(o.selectMode.blockBack)-1]
	o.selectMode.blockBack = o.selectMode.blockBack[:len(o.selectMode.blockBack)-1]
	if state.table {
		o.selectMode.blockMark = false
		o.selectMode.anchorBlock = noBlock
		o.selectMode.activeBlock = noBlock
		o.selectMode.unit = SelectUnitTableCells
		o.selectMode.tableMark = true
		o.selectMode.tableText = false
		o.selectMode.anchorCell = state.tableAnchor
		o.selectMode.activeCell = state.tableActive
		o.selectMode.anchor = o.tableCellPoint(state.tableAnchor)
		o.selectMode.activeEnd = o.tableCellPoint(state.tableActive)
		o.selectMode.marked = true
		o.selectMode.preferredColumn = -1
		return true
	}
	o.installBlockMark(state.anchor, state.active)
	return true
}

// BlockMarkHint is the footer's Markdown block state, mirroring
// Rust transcript_view::select_mode::blocks::block_mark_hints.
func (o *TranscriptOverlay) BlockMarkHint() string {
	if o == nil || !o.selectMode.active || !o.selectMode.blockMark {
		return ""
	}
	label := o.markdownBlockLabel(o.selectMode.activeBlock)
	hint := "Markdown " + label + " \u00b7 \u2190\u2192/hl move \u00b7 \u2191\u2193/jk extend"
	if _, ok := o.markdownBlockTarget(o.selectMode.anchorBlock, o.selectMode.activeBlock); ok {
		hint += " \u00b7 tab expand"
	}
	if len(o.selectMode.blockBack) > 0 {
		hint += " \u00b7 shift+tab shrink"
	}
	return hint + " \u00b7 v mark text \u00b7 y copy \u00b7 esc clear"
}
