package historycell

import (
	"strings"

	"codex_go/tui"
)

// Rust parity: codex-rs/tui/src/history_cell/notices.rs ThreadRecapHistoryCell /
// ThreadRecapLoadingCell.

// RecapHeading labels a generated conversation recap.
const RecapHeading = "Conversation recap"

// ThreadRecapHistoryCell renders a generated recap with a hanging "↳ Recap:"
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

// recapPrefix is Rust's hanging indent and label: "  ↳ Recap: ".
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

// SGR sequences for the rendered recap (Rust #46332): every recap line is
// dimmed while keeping the italic recap style, and the "Recap:"/"Next:" labels
// stay bold without the previous cyan colour. 22 turns bold off, 2/3 restore
// the faint and italic the whole line carries.
const (
	recapLineSGR   = "\x1b[2;3m"
	recapLabelSGR  = "\x1b[1m"
	recapNormalSGR = "\x1b[22;2;3m"
	recapResetSGR  = "\x1b[0m"
)

// StyledDisplayLines renders the recap with Rust's dimmed line style. The plain
// DisplayLines/RawLines contract is unchanged.
func (c ThreadRecapHistoryCell) StyledDisplayLines(width int) []string {
	lines := c.DisplayLines(width)
	styled := make([]string, 0, len(lines))
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			styled = append(styled, line)
			continue
		}
		styled = append(styled, recapLineSGR+recapEmphasizeLabels(line)+recapResetSGR)
	}
	return styled
}

// recapEmphasizeLabels bolds the first recap label on the line, restoring the
// line's faint/italic style after it.
func recapEmphasizeLabels(line string) string {
	for _, label := range []string{"Recap:", "Next:"} {
		index := strings.Index(line, label)
		if index < 0 {
			continue
		}
		end := index + len(label)
		if end < len(line) && line[end] == ' ' {
			end++
		}
		return line[:index] + recapLabelSGR + line[index:end] + recapNormalSGR + line[end:]
	}
	return line
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
