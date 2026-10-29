package markdown

import (
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/parser"
	gmtext "github.com/yuin/goldmark/text"
)

// Rust parity: codex-rs/tui/src/markdown_render/streaming.rs
// (`StreamingMarkdownRender::last_top_level_block_start` and
// `has_reference_link_definition`).

// StreamingMarkdownMetadata mirrors the streaming renderer metadata Rust
// collects during its single parse pass.
type StreamingMarkdownMetadata struct {
	// LastTopLevelBlockStart is the source offset of the final top-level block
	// when at least one earlier block exists.
	LastTopLevelBlockStart int
	// HasLastTopLevelBlock reports whether LastTopLevelBlockStart is meaningful.
	HasLastTopLevelBlock bool
	// HasReferenceLinkDefinition reports whether the source declares a
	// reference-style link definition. Such a definition can retroactively
	// change the rendering of any earlier or later block.
	HasReferenceLinkDefinition bool
}

// AnalyzeStreamingMarkdown collects the block metadata the incremental
// streaming renderer needs. Offsets always index the exact source passed here.
func AnalyzeStreamingMarkdown(source string) StreamingMarkdownMetadata {
	metadata := StreamingMarkdownMetadata{}
	if source == "" {
		return metadata
	}
	data := []byte(source)
	document, context := parseMarkdownDocument(data)
	metadata.HasReferenceLinkDefinition = len(context.References()) > 0

	blockCount := 0
	var lastBlock ast.Node
	for child := document.FirstChild(); child != nil; child = child.NextSibling() {
		blockCount++
		lastBlock = child
	}
	if blockCount <= 1 || lastBlock == nil {
		return metadata
	}
	if start, ok := topLevelBlockLineStart(lastBlock, data); ok {
		metadata.LastTopLevelBlockStart = start
		metadata.HasLastTopLevelBlock = true
	}
	return metadata
}

// HasReferenceLinkDefinition reports whether source declares any reference-style
// link definition (Rust `Parser::reference_definitions()`).
func HasReferenceLinkDefinition(source string) bool {
	if source == "" {
		return false
	}
	_, context := parseMarkdownDocument([]byte(source))
	return len(context.References()) > 0
}

func parseMarkdownDocument(data []byte) (ast.Node, parser.Context) {
	context := parser.NewContext()
	document := goldmark.DefaultParser().Parse(gmtext.NewReader(data), parser.WithContext(context))
	return document, context
}

// topLevelBlockLineStart returns the beginning of the line that opens a
// top-level block, so a list item or blockquote prefix stays inside the block.
// A fenced code block's first leaf line is its content, so the fence opener is
// one line earlier.
func topLevelBlockLineStart(block ast.Node, data []byte) (int, bool) {
	first := -1
	_ = ast.Walk(block, func(node ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		if offset, ok := nodeSourceOffset(node, data); ok && (first < 0 || offset < first) {
			first = offset
		}
		return ast.WalkContinue, nil
	})
	if first < 0 {
		return 0, false
	}
	return lineStartAt(data, first), true
}

// nodeSourceOffset reports the earliest source offset a node carries. Block
// leaf lines start after Markdown prefixes, while table cells and other inline
// containers only expose their text segments.
func nodeSourceOffset(node ast.Node, data []byte) (int, bool) {
	if lines := blockLeafLines(node); lines != nil && lines.Len() > 0 {
		offset := lines.At(0).Start
		if _, fenced := node.(*ast.FencedCodeBlock); fenced {
			offset = previousLineStart(data, offset)
		}
		return offset, true
	}
	switch node := node.(type) {
	case *ast.Text:
		return node.Segment.Start, true
	case *ast.RawHTML:
		if node.Segments != nil && node.Segments.Len() > 0 {
			return node.Segments.At(0).Start, true
		}
	}
	return 0, false
}
