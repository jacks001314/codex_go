package historycell

import (
	"strings"

	"codex_go/tui"
)

// DynamicToolCallCell renders one codex_tui dynamic tool invocation. Retained
// rows are updated by call id so concurrently completing calls keep their
// original transcript position. Rust parity: history_cell/dynamic.rs.
type DynamicToolCallCell struct {
	CallID      string
	Name        string
	Arguments   string
	Status      string
	Output      []string
	DurationMS  *int64
	Interrupted bool
}

// dynamicToolPreviewLines is Rust's DETAIL_PREVIEW_LINES: the compact row keeps
// a bounded tail of the retained output and reports the hidden line count.
const dynamicToolPreviewLines = tui.PreviewLines

func (c DynamicToolCallCell) active() bool {
	status := strings.ToLower(strings.TrimSpace(c.Status))
	return status == "" || status == "inprogress" || status == "in_progress" || status == "running"
}

// header mirrors Rust DynamicToolCallData::header.
func (c DynamicToolCallCell) header() string {
	name := strings.TrimSpace(c.Name)
	if name == "" {
		name = "dynamic tool"
	}
	verb := "Called"
	status := strings.ToLower(strings.TrimSpace(c.Status))
	switch {
	case c.Interrupted:
		verb = "Interrupted"
	case status == "failed" || status == "error":
		verb = "Failed"
	case status == "inprogress" || status == "in_progress" || status == "running" || status == "":
		verb = "Calling"
	}
	heading := "\u2022 " + verb + " " + name
	if c.DurationMS != nil && *c.DurationMS >= 0 {
		heading += " \u00b7 " + itoaHistory(*c.DurationMS) + "ms"
	}
	return heading
}

// outputLines mirrors Rust DynamicToolCallData::output_lines: a retained but
// empty result says so, a call without a result reports it unavailable, and an
// active call has no output row yet.
func (c DynamicToolCallCell) outputLines() []string {
	if c.Output == nil {
		if c.active() {
			return nil
		}
		return []string{"(output unavailable)"}
	}
	lines := make([]string, 0, len(c.Output))
	for _, block := range c.Output {
		block = strings.TrimSpace(block)
		if block == "" {
			continue
		}
		lines = append(lines, rawLinesFromSource(block)...)
	}
	if len(lines) == 0 {
		return []string{"(no output)"}
	}
	return lines
}

func (c DynamicToolCallCell) DisplayLines(width int) []string {
	lines := []string{truncateDynamicLine(c.header(), width)}
	output := c.outputLines()
	skip := 0
	preview := output
	if len(output) > dynamicToolPreviewLines {
		skip = len(output) - dynamicToolPreviewLines
		preview = output[skip:]
	}
	if skip > 0 {
		lines = append(lines, truncateDynamicLine("  \u2026 "+itoaHistory(int64(skip))+" earlier lines hidden", width))
	}
	for index, line := range preview {
		prefix := "    "
		if index == 0 {
			prefix = "  \u2514 "
		}
		lines = append(lines, truncateDynamicLine(prefix+line, width))
	}
	return lines
}

func (c DynamicToolCallCell) RawLines() []string {
	lines := []string{c.header()}
	if args := strings.TrimSpace(c.Arguments); args != "" {
		lines = append(lines, rawLinesFromSource("Arguments: "+args)...)
	}
	return append(lines, c.outputLines()...)
}

// TranscriptLines keeps the complete arguments and output for the expanded
// transcript view (Rust transcript_hyperlink_lines).
func (c DynamicToolCallCell) TranscriptLines(width int) []string {
	lines := []string{c.header()}
	details := []string{}
	if args := strings.TrimSpace(c.Arguments); args != "" {
		details = append(details, rawLinesFromSource("Arguments: "+args)...)
	}
	details = append(details, c.outputLines()...)
	for _, detail := range details {
		lines = append(lines, tui.AdaptiveWrapLine(detail, tui.WrapOptions{
			Width:            max(width, 1),
			InitialIndent:    "  \u2514 ",
			SubsequentIndent: "    ",
			BreakWords:       true,
		})...)
	}
	return lines
}

func (c DynamicToolCallCell) ActivityIDs() []string {
	if strings.TrimSpace(c.CallID) == "" {
		return nil
	}
	return []string{"dynamic:" + strings.TrimSpace(c.CallID)}
}

// ActivityDisclosure reports generic transcript details for a dynamic tool
// call (Rust's default HistoryCell::activity_disclosure).
func (c DynamicToolCallCell) ActivityDisclosure(int) tui.ActivityDisclosure {
	return tui.ActivityDisclosure{Generic: true}
}

func truncateDynamicLine(value string, width int) string {
	if width <= 0 {
		return value
	}
	return tui.TruncateWithEllipsis(value, width)
}

func itoaHistory(value int64) string {
	if value == 0 {
		return "0"
	}
	negative := value < 0
	if negative {
		value = -value
	}
	var digits [20]byte
	index := len(digits)
	for value > 0 {
		index--
		digits[index] = byte('0' + value%10)
		value /= 10
	}
	if negative {
		index--
		digits[index] = '-'
	}
	return string(digits[index:])
}
