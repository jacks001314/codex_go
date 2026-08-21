package markdown

import (
	"strings"

	codextui "codex_go/tui"
)

// Rust parity: codex-rs/tui/src/markdown.rs unwrap_markdown_fences.
//
// A ```markdown fence that wraps a table is unwrapped so the table renders as
// markdown rather than as a code block: the fenced markdown info string makes
// glamour render the content as a code block, but Codex writes tables inside
// fenced markdown to keep them intact. Elsewhere the fence is preserved verbatim.
func UnwrapMarkdownFences(source string) string {
	if !strings.Contains(source, "```") && !strings.Contains(source, "~~~") {
		return source
	}

	type activeKind int
	const (
		activePassthrough activeKind = iota
		activeMarkdown
	)
	type activeFence struct {
		kind          activeKind
		fence         fenceMeta
		openStart     int
		openEnd       int
		contentRanges [][2]int
	}

	var out strings.Builder
	var active *activeFence
	offset := 0
	pushRange := func(start int, end int) {
		if end > start {
			out.WriteString(source[start:end])
		}
	}

	for _, line := range splitInclusiveLines(source) {
		lineStart := offset
		offset += len(line)
		if active != nil {
			a := active
			if a.kind == activePassthrough {
				pushRange(lineStart, offset)
				if !isCloseMarkdownFence(line, a.fence) {
					active = a
				} else {
					active = nil
				}
				continue
			}
			if isCloseMarkdownFence(line, a.fence) {
				if markdownFenceContainsTable(rangesText(source, a.contentRanges), a.fence.blockquoted) {
					for _, r := range a.contentRanges {
						pushRange(r[0], r[1])
					}
				} else {
					pushRange(a.openStart, a.openEnd)
					for _, r := range a.contentRanges {
						pushRange(r[0], r[1])
					}
					pushRange(lineStart, offset)
				}
				active = nil
				continue
			}
			a.contentRanges = append(a.contentRanges, [2]int{lineStart, offset})
			active = a
			continue
		}

		fence, isMarkdown, ok := parseOpenMarkdownFence(line)
		if ok {
			if isMarkdown {
				active = &activeFence{kind: activeMarkdown, fence: fence, openStart: lineStart, openEnd: offset}
			} else {
				pushRange(lineStart, offset)
				active = &activeFence{kind: activePassthrough, fence: fence}
			}
			continue
		}
		pushRange(lineStart, offset)
	}

	if active != nil && active.kind == activeMarkdown {
		pushRange(active.openStart, active.openEnd)
		for _, r := range active.contentRanges {
			pushRange(r[0], r[1])
		}
	}
	return out.String()
}

type fenceMeta struct {
	marker      byte
	length      int
	blockquoted bool
}

func parseOpenMarkdownFence(line string) (fenceMeta, bool, bool) {
	trimmed, ok := stripMarkdownFenceIndent(line)
	if !ok {
		return fenceMeta{}, false, false
	}
	blockquoted := strings.HasPrefix(strings.TrimLeft(trimmed, " \t"), ">")
	scan := codextui.StripBlockquotePrefix(trimmed)
	marker, length, ok := codextui.ParseFenceMarker(scan)
	if !ok {
		return fenceMeta{}, false, false
	}
	isMarkdown := codextui.IsMarkdownFenceInfo(scan, length)
	return fenceMeta{marker: byte(marker), length: length, blockquoted: blockquoted}, isMarkdown, true
}

func isCloseMarkdownFence(line string, fence fenceMeta) bool {
	trimmed, ok := stripMarkdownFenceIndent(line)
	if !ok {
		return false
	}
	scan := trimmed
	if fence.blockquoted {
		if !strings.HasPrefix(strings.TrimLeft(trimmed, " \t"), ">") {
			return false
		}
		scan = codextui.StripBlockquotePrefix(trimmed)
	}
	marker, length, ok := codextui.ParseFenceMarker(scan)
	if !ok {
		return false
	}
	return byte(marker) == fence.marker && length >= fence.length && strings.TrimSpace(scan[length:]) == ""
}

func markdownFenceContainsTable(content string, blockquoted bool) bool {
	var previous string
	hasPrevious := false
	for _, line := range strings.Split(content, "\n") {
		text := line
		if blockquoted {
			text = codextui.StripBlockquotePrefix(line)
		}
		trimmed := strings.TrimSpace(text)
		if trimmed == "" {
			hasPrevious = false
			continue
		}
		if hasPrevious &&
			codextui.IsTableHeaderLine(previous) &&
			!codextui.IsTableDelimiterLine(previous) &&
			codextui.IsTableDelimiterLine(trimmed) {
			return true
		}
		previous = trimmed
		hasPrevious = true
	}
	return false
}

// stripMarkdownFenceIndent strips a trailing newline and up to 3 leading spaces
// (or tabs counting 4 columns), returning the remaining line. It returns false
// for indented code lines (4+ leading spaces) per CommonMark.
func stripMarkdownFenceIndent(line string) (string, bool) {
	withoutNewline := strings.TrimSuffix(line, "\n")
	byteIdx := 0
	column := 0
	for byteIdx < len(withoutNewline) {
		switch withoutNewline[byteIdx] {
		case ' ':
			byteIdx++
			column++
		case '\t':
			byteIdx++
			column += 4
		default:
			return withoutNewline[byteIdx:], true
		}
		if column >= 4 {
			return "", false
		}
	}
	return "", true
}

func rangesText(source string, ranges [][2]int) string {
	var sb strings.Builder
	for _, r := range ranges {
		sb.WriteString(source[r[0]:r[1]])
	}
	return sb.String()
}

func splitInclusiveLines(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			out = append(out, s[start:i+1])
			start = i + 1
		}
	}
	if start < len(s) {
		out = append(out, s[start:])
	}
	return out
}
