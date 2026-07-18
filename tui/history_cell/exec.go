package historycell

import (
	"codex_go/tui"

	"github.com/rivo/uniseg"
)

// Rust parity: codex-rs/tui/src/history_cell/exec.rs.

type UnifiedExecInteractionCell struct {
	CommandDisplay string
	Stdin          string
}

func NewUnifiedExecInteraction(commandDisplay string, stdin string) UnifiedExecInteractionCell {
	return UnifiedExecInteractionCell{CommandDisplay: commandDisplay, Stdin: stdin}
}

func (c UnifiedExecInteractionCell) DisplayLines(width int) []string {
	if width <= 0 {
		return nil
	}
	waitedOnly := c.Stdin == ""
	header := "\u2022 Waited for background terminal"
	if !waitedOnly {
		header = "\u21ab Interacted with background terminal"
	}
	if c.CommandDisplay != "" {
		header += " \u00b7 " + c.CommandDisplay
	}
	lines := tui.AdaptiveWrapLine(header, tui.WrapOptions{Width: width, BreakWords: true})
	if waitedOnly {
		return lines
	}
	for _, raw := range rawLinesFromSource(c.Stdin) {
		lines = append(lines, tui.AdaptiveWrapLine(raw, tui.WrapOptions{
			Width:            width,
			InitialIndent:    "  \u2514 ",
			SubsequentIndent: "    ",
			BreakWords:       true,
		})...)
	}
	return lines
}

func (c UnifiedExecInteractionCell) RawLines() []string {
	if c.Stdin == "" {
		if c.CommandDisplay != "" {
			return []string{"Waited for background terminal: " + c.CommandDisplay}
		}
		return []string{"Waited for background terminal"}
	}
	lines := []string{"Interacted with background terminal"}
	if c.CommandDisplay != "" {
		lines[0] += ": " + c.CommandDisplay
	}
	lines = append(lines, rawLinesFromSource(c.Stdin)...)
	return lines
}

type UnifiedExecProcessDetails struct {
	CommandDisplay string
	RecentChunks   []string
}

type UnifiedExecProcessesCell struct {
	Processes []UnifiedExecProcessDetails
}

func NewUnifiedExecProcessesOutput(processes []UnifiedExecProcessDetails) CompositeHistoryCell {
	parts := []HistoryCell{
		NewPlainHistoryCell([]string{"/ps"}),
		UnifiedExecProcessesCell{Processes: append([]UnifiedExecProcessDetails(nil), processes...)},
	}
	return NewCompositeHistoryCell(parts)
}

func (c UnifiedExecProcessesCell) DisplayLines(width int) []string {
	if width <= 0 {
		return nil
	}
	lines := []string{"Background terminals", ""}
	if len(c.Processes) == 0 {
		return append(lines, "  \u2022 No background terminals running.")
	}
	maxProcesses := 16
	shown := 0
	processPrefix := "  \u2022 "
	processPrefixWidth := tui.DisplayWidth(processPrefix)
	for _, process := range c.Processes {
		if shown >= maxProcesses {
			break
		}
		if width <= processPrefixWidth {
			lines = append(lines, processPrefix)
			shown++
			continue
		}
		snippet, needsSuffix := processCommandSnippet(process.CommandDisplay)
		command := truncateHistoryLine(snippet, width-processPrefixWidth, needsSuffix)
		lines = append(lines, processPrefix+command)
		for index, chunk := range process.RecentChunks {
			prefix := "    \u21b3 "
			if index > 0 {
				prefix = "      "
			}
			prefixWidth := tui.DisplayWidth(prefix)
			if width <= prefixWidth {
				lines = append(lines, prefix)
				continue
			}
			lines = append(lines, prefix+truncateHistoryLine(chunk, width-prefixWidth, false))
		}
		shown++
	}
	if remaining := len(c.Processes) - shown; remaining > 0 {
		more := "... and " + tui.FormatInt(int64(remaining)) + " more running"
		if width <= processPrefixWidth {
			lines = append(lines, processPrefix)
		} else {
			lines = append(lines, processPrefix+tui.TruncateToWidth(more, width-processPrefixWidth))
		}
	}
	return lines
}

func (c UnifiedExecProcessesCell) RawLines() []string {
	return c.DisplayLines(1 << 15)
}

func firstLine(text string) string {
	line, _ := splitFirstLine(text)
	return line
}

func splitFirstLine(text string) (string, bool) {
	for i, r := range text {
		if r == '\n' || r == '\r' {
			return text[:i], true
		}
	}
	return text, false
}

func processCommandSnippet(command string) (string, bool) {
	line, hasMoreLines := splitFirstLine(command)
	graphemes := uniseg.NewGraphemes(line)
	count := 0
	for graphemes.Next() {
		if count == 80 {
			start, _ := graphemes.Positions()
			return line[:start], true
		}
		count++
	}
	return line, hasMoreLines
}

func truncateHistoryLine(text string, width int, forceSuffix bool) string {
	if width <= 0 {
		return ""
	}
	if !forceSuffix && tui.DisplayWidth(text) <= width {
		return text
	}
	const suffix = " [...]"
	suffixWidth := tui.DisplayWidth(suffix)
	if width > suffixWidth {
		return tui.TruncateToWidth(text, width-suffixWidth) + suffix
	}
	return tui.TruncateToWidth(text, width)
}
