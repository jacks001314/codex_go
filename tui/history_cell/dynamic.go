package historycell

import (
	"strings"

	"codex_go/tui"
)

// DynamicToolCallCell renders one codex_tui dynamic tool invocation. Retained
// rows are updated by call id so concurrently completing calls keep their
// original transcript position.
type DynamicToolCallCell struct {
	CallID      string
	Name        string
	Arguments   string
	Status      string
	Output      []string
	DurationMS  *int64
	Interrupted bool
}

func (c DynamicToolCallCell) DisplayLines(width int) []string {
	name := strings.TrimSpace(c.Name)
	if name == "" {
		name = "dynamic tool"
	}
	marker := "\u2022 "
	status := strings.ToLower(strings.TrimSpace(c.Status))
	if c.Interrupted || status == "failed" || status == "error" {
		marker = "\u2717 "
	} else if status == "inprogress" || status == "in_progress" {
		marker = "\u25cb "
	}
	heading := marker + name
	if c.DurationMS != nil && *c.DurationMS > 0 && !c.Interrupted {
		heading += " (" + formatDynamicDuration(*c.DurationMS) + ")"
	}
	lines := []string{truncateDynamicLine(heading, width)}
	if args := strings.TrimSpace(c.Arguments); args != "" {
		lines = append(lines, truncateDynamicLine("  \u2514 "+args, width))
	}
	for _, output := range c.Output {
		output = strings.TrimSpace(output)
		if output == "" {
			continue
		}
		for _, line := range strings.Split(output, "\n") {
			lines = append(lines, truncateDynamicLine("    "+line, width))
		}
	}
	return lines
}

func (c DynamicToolCallCell) RawLines() []string {
	lines := []string{"Dynamic tool call " + strings.TrimSpace(c.Name)}
	if args := strings.TrimSpace(c.Arguments); args != "" {
		lines = append(lines, "arguments: "+args)
	}
	if status := strings.TrimSpace(c.Status); status != "" {
		lines = append(lines, "status: "+status)
	}
	lines = append(lines, c.Output...)
	return lines
}

func (c DynamicToolCallCell) TranscriptLines(width int) []string {
	return c.DisplayLines(width)
}

func (c DynamicToolCallCell) ActivityIDs() []string {
	if strings.TrimSpace(c.CallID) == "" {
		return nil
	}
	return []string{"dynamic-tool:" + strings.TrimSpace(c.CallID)}
}

func truncateDynamicLine(value string, width int) string {
	if width <= 0 {
		return value
	}
	return tui.TruncateWithEllipsis(value, width)
}

func formatDynamicDuration(ms int64) string {
	if ms < 1000 {
		return itoaHistory(ms) + "ms"
	}
	seconds := ms / 1000
	if seconds < 60 {
		return itoaHistory(seconds) + "s"
	}
	minutes := seconds / 60
	return itoaHistory(minutes) + "m " + itoaHistory(seconds%60) + "s"
}

func itoaHistory(value int64) string {
	if value == 0 {
		return "0"
	}
	var digits [20]byte
	index := len(digits)
	for value > 0 {
		index--
		digits[index] = byte('0' + value%10)
		value /= 10
	}
	return string(digits[index:])
}
