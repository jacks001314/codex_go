package markdown

import (
	"strings"

	"codex_go/utils"
)

// collapseBlockBlankLines mirrors Rust's block layout for completed output:
// adjacent blocks are separated by exactly one blank row. Glamour emits an
// extra blank row where a nested list ends and a sibling list begins (and in
// the same position inside blockquotes), while pulldown-cmark in Rust emits a
// single separator. Blank rows inside code blocks are significant and are
// preserved; at this stage fenced blocks are still wrapped by the internal
// source markers.
func collapseBlockBlankLines(rendered string) string {
	if !strings.Contains(rendered, "\n") {
		return rendered
	}
	lines := strings.Split(rendered, "\n")
	out := make([]string, 0, len(lines))
	inCodeBlock := false
	lastBlankDepth := -1
	for _, line := range lines {
		plain := utils.StripANSI(line)
		switch {
		case strings.Contains(plain, codeBlockStartMarker):
			inCodeBlock = true
			lastBlankDepth = -1
		case strings.Contains(plain, codeBlockEndMarker):
			inCodeBlock = false
			lastBlankDepth = -1
		case !inCodeBlock:
			depth, body := splitRenderedQuotePrefix(plain)
			if strings.TrimSpace(body) == "" {
				if depth == lastBlankDepth {
					continue
				}
				lastBlankDepth = depth
			} else {
				lastBlankDepth = -1
			}
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}
