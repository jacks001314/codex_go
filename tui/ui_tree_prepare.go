package tui

// Rust parity: codex-rs/tui/src/ui_tree/render/prepare.rs and
// render/text.rs. Preparation inherits styles down the tree, collapses
// whitespace across styled leaves and measures every row child before layout.

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/rivo/uniseg"
)

// PreparedUITree is a display tree whose inline content is already split into
// styled chunks and measured. It keeps the Rust prepare/layout split so a tree
// can be measured once and rendered at several widths.
type PreparedUITree struct {
	root *preparedUINode
}

// Prepare builds the measured display tree.
func (n *UINode) Prepare() PreparedUITree {
	if n == nil {
		return PreparedUITree{}
	}
	return PreparedUITree{root: prepareUINode(n, uiPaint{})}
}

// uiPaint is the style inherited down one prepared subtree.
type uiPaint struct {
	foreground    UIColor
	hasForeground bool
	background    UIColor
	hasBackground bool
	bold          bool
	italic        bool
	underline     bool
	href          string
}

func (p uiPaint) style() UIStyle {
	style := UIStyle{Bold: p.bold, Italic: p.italic, Underline: p.underline}
	if p.hasForeground {
		style.Foreground = p.foreground
	}
	if p.hasBackground {
		style.Background = p.background
	}
	return style
}

type preparedUIWidths struct {
	minimum   uint64
	preferred uint64
}

type preparedUINode struct {
	text   *preparedUIText
	row    *preparedUIRow
	widths preparedUIWidths
}

type preparedUIRow struct {
	children []*preparedUINode
	gap      uint64
	wrap     UIRowWrap
	paint    uiPaint
}

type preparedUIText struct {
	hasAuthoredBreak bool
	lines            [][]uiChunk
}

type uiRun struct {
	text  string
	paint uiPaint
}

type uiChunk struct {
	runs          []uiRun
	widthCells    int
	trailingSpace *uiPaint
}

type uiInlineText struct {
	text  preparedUIText
	line  []uiRun
	space *uiPaint
}

func (p UITextProps) applyTo(paint *uiPaint) {
	if p.Color != nil {
		paint.foreground = *p.Color
		paint.hasForeground = true
	}
	if p.Weight != nil {
		paint.bold = *p.Weight == UITextWeightBold
	}
	if p.Italic != nil {
		paint.italic = *p.Italic
	}
	if p.Underline != nil {
		paint.underline = *p.Underline
	}
}

func prepareUINode(node *UINode, paint uiPaint) *preparedUINode {
	if node == nil {
		return nil
	}
	items := make([]*preparedUINode, 0, 2)
	inline := &uiInlineText{}
	collectUIInline(&items, inline, node, paint)
	if text := prepareUIText(inline); text != nil {
		items = append(items, text)
	}
	if len(items) == 0 {
		return nil
	}
	if len(items) == 1 {
		return items[0]
	}
	return prepareUIRow(items, 0, UIRowWrapNowrap, paint)
}

func collectUIInline(items *[]*preparedUINode, inline *uiInlineText, node *UINode, paint uiPaint) {
	if node == nil {
		return
	}
	if node.Kind == UINodeString {
		inline.pushLeaf(node.Text, paint)
		return
	}
	if node.Kind == UINodeRow {
		if text := prepareUIText(inline); text != nil {
			*items = append(*items, text)
		}
		*inline = uiInlineText{}
		if node.Background != nil {
			paint.background = *node.Background
			paint.hasBackground = true
		}
		children := make([]*preparedUINode, 0, len(node.Children))
		for _, child := range node.Children {
			if prepared := prepareUINode(child, paint); prepared != nil {
				children = append(children, prepared)
			}
		}
		if row := prepareUIRow(children, node.Gap, node.Wrap, paint); row != nil {
			*items = append(*items, row)
		}
		return
	}
	switch node.Kind {
	case UINodeText:
		node.Props.applyTo(&paint)
	case UINodeLink:
		paint.href = node.Href.String()
		paint.underline = true
	case UINodeBold:
		paint.bold = true
	case UINodeItalic:
		paint.italic = true
	case UINodeUnderline:
		paint.underline = true
	}
	for _, child := range node.Children {
		collectUIInline(items, inline, child, paint)
	}
}

func prepareUIText(inline *uiInlineText) *preparedUINode {
	text := inline.finish()
	if len(text.lines) == 0 {
		return nil
	}
	var widths preparedUIWidths
	for _, line := range text.lines {
		width := 0
		for _, chunk := range line {
			cells := uint64(chunk.widthCells)
			if cells > widths.minimum {
				widths.minimum = cells
			}
			width += chunk.widthCells
			if chunk.trailingSpace != nil {
				width++
			}
		}
		if uint64(width) > widths.preferred {
			widths.preferred = uint64(width)
		}
	}
	return &preparedUINode{text: &text, widths: widths}
}

func prepareUIRow(children []*preparedUINode, gap uint64, wrap UIRowWrap, paint uiPaint) *preparedUINode {
	kept := make([]*preparedUINode, 0, len(children))
	for _, child := range children {
		if child == nil {
			continue
		}
		if child.widths.preferred > 0 || child.row != nil || (child.text != nil && child.text.hasAuthoredBreak) {
			kept = append(kept, child)
		}
	}
	if len(kept) == 0 {
		return nil
	}
	gaps := gap * uint64(len(kept)-1)
	widths := preparedUIWidths{preferred: gaps}
	if wrap == UIRowWrapNowrap {
		widths.minimum = gaps
		for _, child := range kept {
			widths.minimum += child.widths.minimum
		}
	} else {
		for _, child := range kept {
			if child.widths.minimum > widths.minimum {
				widths.minimum = child.widths.minimum
			}
		}
	}
	for _, child := range kept {
		widths.preferred += child.widths.preferred
	}
	return &preparedUINode{
		row:    &preparedUIRow{children: kept, gap: gap, wrap: wrap, paint: paint},
		widths: widths,
	}
}

func (in *uiInlineText) pushLeaf(raw string, paint uiPaint) {
	text := sanitizeUITreeText(boundedUITreeText(raw))
	start := 0
	for index := 0; index < len(text); {
		r, size := utf8.DecodeRuneInString(text[index:])
		if r != ' ' && r != '\t' && r != '\n' {
			index += size
			continue
		}
		if start < index {
			in.pushSpace()
			in.line = append(in.line, uiRun{text: text[start:index], paint: paint})
		}
		if r == '\n' {
			in.text.lines = append(in.text.lines, buildUIChunks(in.line))
			in.line = nil
			in.space = nil
		} else if len(in.line) > 0 && in.space == nil {
			space := paint
			in.space = &space
		}
		index += size
		start = index
	}
	if start < len(text) {
		in.pushSpace()
		in.line = append(in.line, uiRun{text: text[start:], paint: paint})
	}
}

func (in *uiInlineText) pushSpace() {
	if in.space == nil {
		return
	}
	in.line = append(in.line, uiRun{text: " ", paint: *in.space})
	in.space = nil
}

func (in *uiInlineText) finish() preparedUIText {
	in.text.hasAuthoredBreak = len(in.text.lines) > 0
	if len(in.line) > 0 {
		in.text.lines = append(in.text.lines, buildUIChunks(in.line))
	}
	in.line = nil
	in.space = nil
	return in.text
}

type uiGrapheme struct {
	text  string
	paint uiPaint
}

func buildUIChunks(runs []uiRun) []uiChunk {
	graphemes := buildUIGraphemes(runs)
	if len(graphemes) == 0 {
		return nil
	}
	chunks := make([]uiChunk, 0, 4)
	chunk := uiChunk{}
	var previous rune
	wordHyphen := false
	for index, grapheme := range graphemes {
		if index > 0 {
			first, _ := utf8.DecodeRuneInString(grapheme.text)
			canBreak := false
			switch previous {
			case '-':
				canBreak = wordHyphen && isUIAlphanumeric(first)
			case '\u00ad':
				canBreak = false
			default:
				canBreak = uiBreakOpportunity(previous, first)
			}
			if canBreak {
				chunks = appendChunkBreak(chunks, &chunk)
			}
		}
		chunk.appendGrapheme(grapheme.text, grapheme.paint)
		for _, ch := range grapheme.text {
			wordHyphen = ch == '-' && isUIAlphanumeric(previous)
			previous = ch
		}
	}
	if len(chunk.runs) > 0 {
		chunks = append(chunks, chunk)
	}
	return chunks
}

func appendChunkBreak(chunks []uiChunk, chunk *uiChunk) []uiChunk {
	if len(chunk.runs) == 0 {
		return chunks
	}
	if last := len(chunk.runs) - 1; chunk.runs[last].text == " " {
		paint := chunk.runs[last].paint
		chunk.trailingSpace = &paint
		chunk.runs = chunk.runs[:last]
		chunk.widthCells--
	}
	if len(chunk.runs) > 0 {
		chunks = append(chunks, *chunk)
	}
	*chunk = uiChunk{}
	return chunks
}

func (chunk *uiChunk) appendGrapheme(text string, paint uiPaint) {
	if last := len(chunk.runs) - 1; last >= 0 && text != " " && chunk.runs[last].text != " " && chunk.runs[last].paint == paint {
		chunk.runs[last].text += text
	} else {
		chunk.runs = append(chunk.runs, uiRun{text: text, paint: paint})
	}
	chunk.widthCells += DisplayWidth(text)
}

// buildUIGraphemes segments the concatenated leaves together so a cluster that
// spans style boundaries keeps the first style and only keeps its hyperlink
// when every contributing leaf agrees on it.
func buildUIGraphemes(runs []uiRun) []uiGrapheme {
	if len(runs) == 0 {
		return nil
	}
	var combined strings.Builder
	type segment struct {
		start int
		end   int
		paint uiPaint
	}
	segments := make([]segment, 0, len(runs))
	for _, run := range runs {
		start := combined.Len()
		combined.WriteString(run.text)
		segments = append(segments, segment{start: start, end: combined.Len(), paint: run.paint})
	}
	text := combined.String()
	if text == "" {
		return nil
	}
	graphemes := make([]uiGrapheme, 0, len(text))
	cursor := 0
	next := uniseg.NewGraphemes(text)
	for next.Next() {
		start, end := next.Positions()
		for cursor+1 < len(segments) && segments[cursor].end <= start {
			cursor++
		}
		paint := segments[cursor].paint
		for index := cursor; index < len(segments) && segments[index].start < end; index++ {
			if segments[index].paint.href != paint.href {
				paint.href = ""
				break
			}
		}
		graphemes = append(graphemes, uiGrapheme{text: text[start:end], paint: paint})
	}
	return graphemes
}

func uiBreakOpportunity(previous rune, current rune) bool {
	if previous == ' ' || previous == '\t' {
		return true
	}
	if previous == '\u00a0' || current == '\u00a0' {
		return false
	}
	if uiIsCJK(previous) || uiIsCJK(current) {
		return true
	}
	return false
}

func uiIsCJK(r rune) bool {
	return unicode.Is(unicode.Han, r) ||
		unicode.Is(unicode.Hiragana, r) ||
		unicode.Is(unicode.Katakana, r) ||
		unicode.Is(unicode.Hangul, r) ||
		(r >= 0x2e80 && r <= 0x2fff)
}

func isUIAlphanumeric(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r)
}
