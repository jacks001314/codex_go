package historycell

import (
	"strings"

	"codex_go/tui"
)

// Rust parity: codex-rs/tui/src/history_cell/notices.rs ThreadRecapHistoryCell /
// ThreadRecapLoadingCell.

// RecapHeading labels a generated conversation recap.
const RecapHeading = "Conversation recap"

// ThreadRecapHistoryCell renders a generated recap with a hanging "鈫?Recap:"
// indent and an optional separately-styled "Next:" line (Rust
// #45089/#45090 notices.rs ThreadRecapHistoryCell).
type ThreadRecapHistoryCell struct {
	Recap      string
	NextAction *string
}

// NewThreadRecapHistoryCell builds a recap cell from the generated summary.
func NewThreadRecapHistoryCell(recap string) ThreadRecapHistoryCell {
	return ThreadRecapHistoryCell{Recap: recap}
}

// WithNextAction attaches the optional next action rendered as a separate
// "Next:" line. A nil action leaves the cell unchanged.
func (c ThreadRecapHistoryCell) WithNextAction(nextAction *string) ThreadRecapHistoryCell {
	c.NextAction = nextAction
	return c
}

// recapPrefix is Rust's hanging indent and label: "  鈫?Recap: ".
const recapPrefix = "  \u21b3 Recap: "

func (c ThreadRecapHistoryCell) DisplayLines(width int) []string {
	if width <= 0 {
		return nil
	}
	wrapWidth := max(width-2, 1)
	body := rawLinesFromSource(c.Recap)
	if c.NextAction != nil && strings.TrimSpace(*c.NextAction) != "" {
		actionLines := rawLinesFromSource(*c.NextAction)
		if len(actionLines) > 0 {
			actionLines[0] = "Next: " + actionLines[0]
		}
		body = append(body, actionLines...)
	}
	prefixWidth := tui.DisplayWidth(recapPrefix)
	if wrapWidth <= prefixWidth {
		// Keep the text readable when the terminal cannot fit the hanging
		// indent: a standalone header followed by unindented body lines.
		lines := []string{"\u21b3 Recap:"}
		return append(lines, wrapRecapBody(body, tui.WrapOptions{Width: wrapWidth})...)
	}
	return wrapRecapBody(body, tui.WrapOptions{
		Width:            wrapWidth,
		InitialIndent:    recapPrefix,
		SubsequentIndent: strings.Repeat(" ", prefixWidth),
	})
}

// wrapRecapBody wraps each source line with the shared adaptive wrapper, then
// re-wraps any line that still exceeds the width with word breaking, matching
// Rust's oversized-token fallback (url-preserving options with break_words).
func wrapRecapBody(body []string, options tui.WrapOptions) []string {
	var lines []string
	for index, source := range body {
		lineOptions := options
		if index > 0 {
			lineOptions.InitialIndent = options.SubsequentIndent
		}
		wrapped := tui.AdaptiveWrapLine(source, lineOptions)
		exceeds := false
		for _, line := range wrapped {
			if tui.DisplayWidth(line) > lineOptions.Width {
				exceeds = true
				break
			}
		}
		if exceeds {
			fallback := lineOptions
			fallback.BreakWords = true
			fallback.PreserveURLs = false
			wrapped = tui.WrapLine(source, fallback)
		}
		lines = append(lines, wrapped...)
	}
	return lines
}

func (c ThreadRecapHistoryCell) RawLines() []string {
	lines := []string{RecapHeading}
	lines = append(lines, rawLinesFromSource(c.Recap)...)
	if c.NextAction != nil && strings.TrimSpace(*c.NextAction) != "" {
		actionLines := rawLinesFromSource(*c.NextAction)
		if len(actionLines) > 0 {
			actionLines[0] = "Next: " + actionLines[0]
		}
		lines = append(lines, actionLines...)
	}
	return lines
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
