package markdown

import (
	"sort"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	gmtext "github.com/yuin/goldmark/text"
)

// revealHeadingMarkers inserts the ATX marker text ("# ", "## ", ...) at the
// start of every heading's content. Rust's markdown renderer paints those
// markers as part of the heading (codex-rs/tui/src/markdown_render.rs,
// start_heading), so "## Summary" stays visible instead of collapsing to a
// styled "Summary". Inserting into the source keeps the markers inside the
// heading span, so they inherit heading styling and wrap with the heading.
//
// Parsing with goldmark covers ATX and setext headings, nested containers
// (blockquotes, list items) and skips fenced or indented code blocks.
func revealHeadingMarkers(source string) string {
	if !strings.Contains(source, "#") && !strings.Contains(source, "\n=") && !strings.Contains(source, "\n-") {
		return source
	}
	data := []byte(source)
	document := goldmark.DefaultParser().Parse(gmtext.NewReader(data))
	var edits []headingMarkerEdit
	_ = ast.Walk(document, func(node ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		heading, ok := node.(*ast.Heading)
		if !ok || heading.Level < 1 || heading.Level > 6 {
			return ast.WalkContinue, nil
		}
		lines := heading.Lines()
		if lines.Len() == 0 {
			return ast.WalkContinue, nil
		}
		offset := lines.At(0).Start
		if offset < 0 || offset > len(source) {
			return ast.WalkContinue, nil
		}
		edits = append(edits, headingMarkerEdit{
			offset: offset,
			marker: strings.Repeat("#", heading.Level) + " ",
		})
		// Glamour's parser leaves setext headings unstyled, so turn them into
		// ATX headings: the inserted marker makes the content line an ATX
		// heading, and the underline is removed.
		if markup := source[lineStart(source, offset):offset]; strings.TrimSpace(markup) == "" {
			last := lines.At(lines.Len() - 1)
			underlineStart, underlineEnd := nextLineBounds(source, last.Stop)
			if setextUnderline(source[underlineStart:underlineEnd]) {
				// The first marker converts the setext content into an ATX
				// heading; the second one is the visible marker Rust paints.
				edits[len(edits)-1].marker += edits[len(edits)-1].marker
				edits = append(edits, headingMarkerEdit{removeStart: underlineStart, removeEnd: underlineEnd})
			}
		}
		return ast.WalkContinue, nil
	})
	if len(edits) == 0 {
		return source
	}
	sort.Sort(sort.Reverse(byHeadingEditOffset(edits)))
	for _, edit := range edits {
		if edit.removeEnd > edit.removeStart {
			source = source[:edit.removeStart] + source[edit.removeEnd:]
			continue
		}
		source = source[:edit.offset] + edit.marker + source[edit.offset:]
	}
	return source
}

// headingMarkerEdit inserts marker text at offset, or removes
// [removeStart, removeEnd) when that range is non-empty.
type headingMarkerEdit struct {
	offset      int
	marker      string
	removeStart int
	removeEnd   int
}

type byHeadingEditOffset []headingMarkerEdit

func (a byHeadingEditOffset) Len() int { return len(a) }
func (a byHeadingEditOffset) Less(i, j int) bool {
	return a[i].sortOffset() < a[j].sortOffset()
}
func (a byHeadingEditOffset) Swap(i, j int) { a[i], a[j] = a[j], a[i] }

func (e headingMarkerEdit) sortOffset() int {
	if e.removeEnd > e.removeStart {
		return e.removeStart
	}
	return e.offset
}

func lineStart(source string, offset int) int {
	return strings.LastIndexByte(source[:offset], '\n') + 1
}

func nextLineBounds(source string, offset int) (int, int) {
	start := strings.IndexByte(source[offset:], '\n')
	if start < 0 {
		return len(source), len(source)
	}
	start += offset + 1
	end := strings.IndexByte(source[start:], '\n')
	if end < 0 {
		return start, len(source)
	}
	return start, start + end
}

func setextUnderline(line string) bool {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return false
	}
	marker := trimmed[0]
	if marker != '=' && marker != '-' {
		return false
	}
	for index := 1; index < len(trimmed); index++ {
		if trimmed[index] != marker {
			return false
		}
	}
	return true
}
