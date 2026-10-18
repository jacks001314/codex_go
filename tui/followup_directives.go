package tui

import (
	"sort"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/text"
)

// FollowupLabels replaces assistant-authored follow-up directives with their
// visible Markdown labels while leaving code spans, fenced code, HTML, links,
// images, escaped markers and malformed directives literal.
//
// Rust parity: codex-rs/tui/src/markdown_render/inline_directives.rs
// `followup_labels` (#49089).
func FollowupLabels(input string) string {
	if !strings.Contains(input, ":codex-followup[") {
		return input
	}
	literals := followupLiteralRanges(input)
	type replacement struct {
		start int
		end   int
		label string
	}
	replacements := make([]replacement, 0)
	literalIndex := 0
	end := 0
	for offset := 0; offset < len(input); {
		relative := strings.Index(input[offset:], ":codex-followup[")
		if relative < 0 {
			break
		}
		start := offset + relative
		if start < end {
			offset = end
			continue
		}
		for literalIndex < len(literals) && literals[literalIndex].end <= start {
			literalIndex++
		}
		if literalIndex < len(literals) && literals[literalIndex].start <= start && start < literals[literalIndex].end {
			offset = start + 1
			continue
		}
		if escapedDirectiveMarker(input, start) {
			offset = start + 1
			continue
		}
		directive, ok := ParseAssistantDirective(input[start:], QuoteEscapingBackslash)
		if !ok || directive.Name != "codex-followup" || !strings.HasPrefix(directive.Raw, ":codex-followup[") || strings.TrimSpace(directive.Label) == "" {
			offset = start + 1
			continue
		}
		directiveEnd := start + len(directive.Raw)
		if directiveEnd > len(input) {
			offset = start + 1
			continue
		}
		replacements = append(replacements, replacement{start: start, end: directiveEnd, label: directive.Label})
		end = directiveEnd
		offset = directiveEnd
	}
	if len(replacements) == 0 {
		return input
	}
	var output strings.Builder
	output.Grow(len(input))
	offset := 0
	for _, replacement := range replacements {
		output.WriteString(input[offset:replacement.start])
		output.WriteString(replacement.label)
		offset = replacement.end
	}
	output.WriteString(input[offset:])
	return output.String()
}

// HideUnfinishedFollowupDirective removes an unterminated follow-up directive
// from the disposable streaming preview. Completed or malformed directives are
// left untouched for the ordinary literal-preservation rules.
func HideUnfinishedFollowupDirective(input string) string {
	start := strings.LastIndex(input, ":codex-followup[")
	if start < 0 || strings.Contains(input[start:], "\n") || escapedDirectiveMarker(input, start) || insideOpenCodeFence(input[:start]) {
		return input
	}
	if _, ok := ParseAssistantDirective(input[start:], QuoteEscapingBackslash); ok {
		return input
	}
	return strings.TrimRight(input[:start], " \t")
}

func insideOpenCodeFence(prefix string) bool {
	fence := byte(0)
	for _, line := range strings.Split(prefix, "\n") {
		line = strings.TrimLeft(line, " \t")
		if len(line) < 3 {
			continue
		}
		marker := line[0]
		if marker != '`' && marker != '~' {
			continue
		}
		run := 0
		for run < len(line) && line[run] == marker {
			run++
		}
		if run < 3 {
			continue
		}
		if fence == 0 {
			fence = marker
		} else if fence == marker {
			fence = 0
		}
	}
	return fence != 0
}

type sourceRange struct {
	start int
	end   int
}

func followupLiteralRanges(input string) []sourceRange {
	parser := goldmark.New(goldmark.WithExtensions(extension.GFM)).Parser()
	document := parser.Parse(text.NewReader([]byte(input)))
	ranges := make([]sourceRange, 0)
	_ = ast.Walk(document, func(node ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch node := node.(type) {
		case *ast.FencedCodeBlock:
			ranges = append(ranges, codeBlockRanges(node.Lines())...)
		case *ast.CodeBlock:
			ranges = append(ranges, codeBlockRanges(node.Lines())...)
		case *ast.HTMLBlock:
			ranges = append(ranges, codeBlockRanges(node.Lines())...)
			if node.HasClosure() {
				ranges = append(ranges, sourceRange{start: node.ClosureLine.Start, end: node.ClosureLine.Stop})
			}
		case *ast.CodeSpan, *ast.Link, *ast.Image:
			ranges = append(ranges, descendantTextRanges(node)...)
		case *ast.RawHTML:
			for index := 0; node.Segments != nil && index < node.Segments.Len(); index++ {
				segment := node.Segments.At(index)
				ranges = append(ranges, sourceRange{start: segment.Start, end: segment.Stop})
			}
		}
		return ast.WalkContinue, nil
	})
	if len(ranges) == 0 {
		return nil
	}
	sort.Slice(ranges, func(i, j int) bool {
		if ranges[i].start == ranges[j].start {
			return ranges[i].end < ranges[j].end
		}
		return ranges[i].start < ranges[j].start
	})
	out := ranges[:0]
	for _, current := range ranges {
		if current.start >= current.end {
			continue
		}
		if len(out) == 0 || current.start > out[len(out)-1].end {
			out = append(out, current)
			continue
		}
		if current.end > out[len(out)-1].end {
			out[len(out)-1].end = current.end
		}
	}
	return out
}

func codeBlockRanges(lines *text.Segments) []sourceRange {
	if lines == nil {
		return nil
	}
	out := make([]sourceRange, 0, lines.Len())
	for index := 0; index < lines.Len(); index++ {
		segment := lines.At(index)
		out = append(out, sourceRange{start: segment.Start, end: segment.Stop})
	}
	return out
}

func descendantTextRanges(node ast.Node) []sourceRange {
	if node == nil {
		return nil
	}
	out := make([]sourceRange, 0)
	_ = ast.Walk(node, func(child ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering || child == node {
			return ast.WalkContinue, nil
		}
		switch child := child.(type) {
		case *ast.Text:
			out = append(out, sourceRange{start: child.Segment.Start, end: child.Segment.Stop})
		}
		return ast.WalkContinue, nil
	})
	return out
}

func escapedDirectiveMarker(input string, start int) bool {
	backslashes := 0
	for index := start - 1; index >= 0 && input[index] == '\\'; index-- {
		backslashes++
	}
	return backslashes%2 == 1
}
