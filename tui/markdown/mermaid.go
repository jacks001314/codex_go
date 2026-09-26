package markdown

import (
	"strings"

	"codex_go/mermaid"
	codextui "codex_go/tui"
)

// Rust parity: codex-rs/tui/src/markdown_render/mermaid.rs. Bounded,
// theme-aware Mermaid previews for completed Markdown fences: the source stays
// owned by the transcript, and unsupported syntax, resource limits and terminal
// overflow keep the original code block behind a reason notice.

// mermaidFallbackWidth mirrors Rust's `width.unwrap_or(120)`.
const mermaidFallbackWidth = 120

const (
	mermaidUnsupportedNotice = "This Mermaid diagram uses features the terminal renderer doesn't support."
	mermaidTooWideNotice     = "This Mermaid diagram doesn't fit the current terminal width."
	mermaidLimitNotice       = "This Mermaid diagram exceeds the terminal renderer's size limits."
)

func mermaidFallbackNotice(err error) string {
	switch err {
	case mermaid.ErrTooWide:
		return mermaidTooWideNotice
	case mermaid.ErrLimit:
		return mermaidLimitNotice
	default:
		return mermaidUnsupportedNotice
	}
}

// renderMermaidFence mirrors `mermaid::render`: a completed fence renders as a
// bounded diagram; an unsupported, over-wide or over-limit diagram keeps the
// original source behind a dimmed reason notice.
func renderMermaidFence(source string, width int, themeID string) []string {
	if width <= 0 {
		width = mermaidFallbackWidth
	}
	spans, err := mermaid.RenderSpans(source, width)
	if err != nil {
		lines := wrapDimmedNotice(mermaidFallbackNotice(err), width)
		return append(lines, highlightedMermaidSource(source, themeID)...)
	}
	return styleMermaidSpans(spans, themeID)
}

// styleMermaidSpans maps each semantic role onto the theme, mirroring
// `mermaid::render`: node colour from `entity.name.type`/`support.type`/
// `variable`, edge colour from `comment`, falling back to Rust's cyan and dim
// when the theme styles neither scope. Text is unstyled.
func styleMermaidSpans(diagram [][]mermaid.Span, themeID string) []string {
	nodeSGR := codextui.ThemeScopeForegroundSGR(themeID, "entity.name.type", "support.type", "variable")
	if nodeSGR == "" {
		nodeSGR = "\x1b[36m"
	}
	edgeSGR := codextui.ThemeScopeForegroundSGR(themeID, "comment")
	if edgeSGR == "" {
		edgeSGR = "\x1b[2m"
	}
	lines := make([]string, 0, len(diagram))
	for _, spans := range diagram {
		var builder strings.Builder
		for _, span := range spans {
			switch span.Role {
			case mermaid.RoleNode:
				builder.WriteString(nodeSGR)
				builder.WriteString(span.Text)
				builder.WriteString("\x1b[0m")
			case mermaid.RoleEdge:
				builder.WriteString(edgeSGR)
				builder.WriteString(span.Text)
				builder.WriteString("\x1b[0m")
			default:
				builder.WriteString(span.Text)
			}
		}
		lines = append(lines, builder.String())
	}
	return lines
}

// highlightedMermaidSource renders the preserved source as a Mermaid code block,
// exactly as an unsupported diagram would be highlighted without the notice.
func highlightedMermaidSource(source string, themeID string) []string {
	highlighted := codextui.HighlightCodeANSI(source, "mermaid", themeID)
	if highlighted == "" {
		return nil
	}
	return strings.Split(strings.ReplaceAll(highlighted, "\r\n", "\n"), "\n")
}

// wrapDimmedNotice word-wraps the notice to the block's width and dims every
// line (Rust `textwrap::wrap` + `Line::from(line.dim())`).
func wrapDimmedNotice(notice string, width int) []string {
	if width < 1 {
		width = 1
	}
	wrapped := wrapText(notice, width)
	lines := make([]string, 0, len(wrapped))
	for _, line := range wrapped {
		lines = append(lines, "\x1b[2m"+line+"\x1b[0m")
	}
	return lines
}

// wrapText greedily wraps whitespace-separated words to the given width; a word
// longer than the width occupies its own line.
func wrapText(text string, width int) []string {
	words := strings.Fields(text)
	if len(words) == 0 {
		return nil
	}
	lines := []string{}
	current := words[0]
	for _, word := range words[1:] {
		if len(current)+1+len(word) <= width {
			current += " " + word
			continue
		}
		lines = append(lines, current)
		current = word
	}
	return append(lines, current)
}

// mermaidHasClosingFence mirrors `mermaid::has_closing_fence`: CommonMark emits
// an End event even at EOF, so a completed fence is only one whose closing
// marker (a run of the opening marker at least as long) follows the content.
// contentStart/contentEnd are byte offsets into source; the opening line may
// carry a blockquote prefix, which is skipped before counting markers.
func mermaidHasClosingFence(source string, contentStart, contentEnd int) bool {
	if contentStart <= 0 || contentStart > contentEnd || contentEnd > len(source) {
		return false
	}
	openingLine := lineBefore(source, contentStart)
	marker, openingLen := fenceOpeningRun(openingLine)
	if openingLen == 0 {
		return false
	}
	position := contentEnd
	if position < len(source) && source[position] == '\n' {
		position++
	}
	closingLine := lineAt(source, position)
	closingMarker, closingLen := fenceClosingRun(closingLine)
	return closingMarker == marker && closingLen >= openingLen
}

// lineBefore returns the line that ends just before offset, excluding the
// newline that terminates it.
func lineBefore(source string, offset int) string {
	end := offset
	if end > 0 && source[end-1] == '\n' {
		end--
	}
	if end > 0 && source[end-1] == '\r' {
		end--
	}
	start := strings.LastIndexByte(source[:end], '\n') + 1
	return source[start:end]
}

// lineAt returns the line starting at offset, without its trailing newline.
func lineAt(source string, offset int) string {
	if offset >= len(source) {
		return ""
	}
	rest := source[offset:]
	if index := strings.IndexByte(rest, '\n'); index >= 0 {
		rest = rest[:index]
	}
	return strings.TrimSuffix(rest, "\r")
}

// fenceOpeningRun returns the marker byte and leading run length of an opening
// fence line after skipping indentation and blockquote prefixes. The info string
// that follows the run is allowed.
func fenceOpeningRun(line string) (byte, int) {
	trimmed := skipFencePrefixes(line)
	if trimmed == "" || (trimmed[0] != '`' && trimmed[0] != '~') {
		return 0, 0
	}
	marker := trimmed[0]
	count := 0
	for count < len(trimmed) && trimmed[count] == marker {
		count++
	}
	return marker, count
}

// fenceClosingRun returns the marker byte and run length of a closing fence
// line: after the prefixes the remaining text is all markers, so ordinary
// content never looks like a closer.
func fenceClosingRun(line string) (byte, int) {
	trimmed := skipFencePrefixes(line)
	if trimmed == "" || (trimmed[0] != '`' && trimmed[0] != '~') {
		return 0, 0
	}
	marker := trimmed[0]
	count := 0
	for count < len(trimmed) && trimmed[count] == marker {
		count++
	}
	if strings.TrimSpace(trimmed[count:]) != "" {
		return 0, 0
	}
	return marker, count
}

func skipFencePrefixes(line string) string {
	trimmed := strings.TrimLeft(line, " \t")
	for strings.HasPrefix(trimmed, ">") {
		trimmed = strings.TrimPrefix(trimmed, ">")
		trimmed = strings.TrimLeft(trimmed, " \t")
	}
	return trimmed
}
