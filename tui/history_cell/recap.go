package historycell

import (
	"strings"

	"codex_go/tui"
)

// Rust parity: codex-rs/tui/src/history_cell/notices.rs ThreadRecapHistoryCell /
// ThreadRecapLoadingCell.

// RecapHeading labels a generated conversation recap.
const RecapHeading = "Conversation recap"

// ThreadRecapHistoryCell renders a generated recap as a labeled checkpoint row
// followed by the indented recap body.
type ThreadRecapHistoryCell struct {
	Recap string
}

// NewThreadRecapHistoryCell builds a recap cell from the generated text.
func NewThreadRecapHistoryCell(recap string) ThreadRecapHistoryCell {
	return ThreadRecapHistoryCell{Recap: recap}
}

func (c ThreadRecapHistoryCell) DisplayLines(width int) []string {
	wrapWidth := width - 2
	if wrapWidth < 1 {
		wrapWidth = 1
	}
	body := tui.ReflowTranscriptLines(rawLinesFromSource(c.Recap), wrapWidth)
	lines := []string{recapHeadingLine(width), ""}
	for _, line := range body {
		lines = append(lines, "  "+line)
	}
	return lines
}

func (c ThreadRecapHistoryCell) RawLines() []string {
	lines := []string{RecapHeading}
	return append(lines, rawLinesFromSource(c.Recap)...)
}

// ThreadRecapLoadingCell is the transient "Generating conversation recap" row
// shown while a manual recap runs.
type ThreadRecapLoadingCell struct{}

// NewThreadRecapLoadingCell builds the loading row.
func NewThreadRecapLoadingCell() ThreadRecapLoadingCell {
	return ThreadRecapLoadingCell{}
}

func (c ThreadRecapLoadingCell) DisplayLines(width int) []string {
	_ = width
	return []string{"\u2022 Generating conversation recap\u2026"}
}

func (c ThreadRecapLoadingCell) RawLines() []string {
	return []string{"Generating conversation recap..."}
}

// recapHeadingLine builds "─ Conversation recap ───…" padded to width, matching
// Rust's take_prefix_by_width heading layout.
func recapHeadingLine(width int) string {
	if width <= 0 {
		return ""
	}
	var builder strings.Builder
	remaining := width
	if remaining > 0 {
		builder.WriteString("\u2500")
		remaining--
	}
	if remaining > 0 {
		builder.WriteString(" ")
		remaining--
	}
	if visible := tui.TruncateToWidth(RecapHeading, remaining); visible != "" {
		builder.WriteString(visible)
		remaining -= tui.DisplayWidth(visible)
	}
	if remaining > 0 {
		builder.WriteString(" ")
		remaining--
	}
	if remaining > 0 {
		builder.WriteString(strings.Repeat("\u2500", remaining))
	}
	return builder.String()
}
