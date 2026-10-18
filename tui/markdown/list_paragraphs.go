package markdown

import (
	"sort"
	"strings"

	"codex_go/utils"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	gmtext "github.com/yuin/goldmark/text"
)

// Private-use runes never appear in normal transcript text and are short
// enough to avoid wrapping at narrow widths before the marker is replaced.
const codexListParagraphBreak = "\uE000\uE001"

// codexListParagraphBreakSentinel marks the blank row that separates two
// paragraphs of the same list item. It is stripped after spacing is applied.
const codexListParagraphBreakSentinel = "\u200B"

// insertListParagraphBreaks turns the blank line between two paragraphs of one
// list item into a private heading marker. Glamour otherwise concatenates those
// paragraphs without a newline. The marker renders on its own row and is
// replaced with a blank line after rendering (Rust #46899).
func insertListParagraphBreaks(source string) string {
	if !strings.Contains(source, "\n") {
		return source
	}
	data := []byte(source)
	document := goldmark.DefaultParser().Parse(gmtext.NewReader(data))
	insertions := []int{}
	_ = ast.Walk(document, func(node ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		item, ok := node.(*ast.ListItem)
		if !ok {
			return ast.WalkContinue, nil
		}
		paragraphs := []*ast.Paragraph{}
		for child := item.FirstChild(); child != nil; child = child.NextSibling() {
			if paragraph, ok := child.(*ast.Paragraph); ok {
				paragraphs = append(paragraphs, paragraph)
			}
		}
		if len(paragraphs) < 2 {
			return ast.WalkContinue, nil
		}
		for _, paragraph := range paragraphs[1:] {
			lines := paragraph.Lines()
			if lines.Len() == 0 {
				continue
			}
			contentStart := lines.At(0).Start
			if contentStart <= 0 || contentStart > len(source) {
				continue
			}
			lineStart := strings.LastIndexByte(source[:contentStart], '\n') + 1
			insertions = append(insertions, lineStart)
		}
		return ast.WalkContinue, nil
	})
	if len(insertions) == 0 {
		return source
	}
	sort.Sort(sort.Reverse(sort.IntSlice(insertions)))
	last := -1
	for _, offset := range insertions {
		if offset == last {
			continue
		}
		last = offset
		lineEnd := strings.IndexByte(source[offset:], '\n')
		if lineEnd < 0 {
			lineEnd = len(source) - offset
		}
		prefix := source[offset : offset+lineEnd]
		prefix = prefix[:len(prefix)-len(strings.TrimLeft(prefix, " \t"))]
		source = source[:offset] + prefix + "#### " + codexListParagraphBreak + "\n\n" + source[offset:]
	}
	return source
}

// replaceListParagraphBreakMarkers converts the private heading rows into the
// blank separators Rust paints between list-item paragraphs.
func replaceListParagraphBreakMarkers(rendered string) string {
	if !strings.Contains(rendered, codexListParagraphBreak) {
		return rendered
	}
	lines := strings.Split(rendered, "\n")
	for index, line := range lines {
		quoteDepth, text := splitRenderedQuotePrefix(strings.TrimSpace(utils.StripANSI(line)))
		if strings.TrimSpace(text) != codexListParagraphBreak {
			continue
		}
		lines[index] = renderedQuoteBlank(quoteDepth) + codexListParagraphBreakSentinel
	}
	return strings.Join(lines, "\n")
}
