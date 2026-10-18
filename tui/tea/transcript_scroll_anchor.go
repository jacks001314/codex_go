package tea

import (
	"strings"

	"codex_go/utils"
)

// transcriptScrollAnchor keeps the reader's top visible content stable when a
// streamed cell consolidates and re-wraps into a different number of rows
// (Rust #46899). It records the first non-blank visible line and how far below
// the viewport top it sat.
type transcriptScrollAnchor struct {
	line      string
	offset    int
	yOffset   int
	hasAnchor bool
}

func captureTranscriptScrollAnchor(content string, yOffset, height int) transcriptScrollAnchor {
	anchor := transcriptScrollAnchor{yOffset: max(yOffset, 0)}
	if strings.TrimSpace(content) == "" {
		return anchor
	}
	lines := strings.Split(content, "\n")
	if yOffset < 0 || yOffset >= len(lines) {
		return anchor
	}
	if height <= 0 {
		height = 1
	}
	end := min(len(lines), yOffset+height)
	for index := yOffset; index < end; index++ {
		line := normalizeTranscriptAnchorLine(lines[index])
		if line == "" {
			continue
		}
		anchor.line = line
		anchor.offset = index - yOffset
		anchor.hasAnchor = true
		return anchor
	}
	return anchor
}

func restoreTranscriptScrollAnchor(content string, anchor transcriptScrollAnchor, maxOffset int) int {
	if !anchor.hasAnchor {
		return clampTranscriptOffset(anchor.yOffset, maxOffset)
	}
	lines := strings.Split(content, "\n")
	best := -1
	for index, line := range lines {
		if normalizeTranscriptAnchorLine(line) != anchor.line {
			continue
		}
		if best < 0 || transcriptAnchorDistance(index, anchor.yOffset) < transcriptAnchorDistance(best, anchor.yOffset) {
			best = index
		}
	}
	if best < 0 {
		return clampTranscriptOffset(anchor.yOffset, maxOffset)
	}
	return clampTranscriptOffset(best-anchor.offset, maxOffset)
}

func normalizeTranscriptAnchorLine(line string) string {
	return strings.TrimRight(utils.StripANSI(line), " \t")
}

func transcriptAnchorDistance(index, yOffset int) int {
	if index >= yOffset {
		return index - yOffset
	}
	return yOffset - index
}

func clampTranscriptOffset(offset, maxOffset int) int {
	if offset < 0 {
		return 0
	}
	if maxOffset >= 0 && offset > maxOffset {
		return maxOffset
	}
	return offset
}
