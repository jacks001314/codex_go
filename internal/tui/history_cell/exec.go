package historycell

import "codex_go/internal/tui"

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
		return append(lines, "  - No background terminals running.")
	}
	maxProcesses := 16
	shown := 0
	for _, process := range c.Processes {
		if shown >= maxProcesses {
			break
		}
		command := truncateHistoryLine(firstLine(process.CommandDisplay), width-4)
		lines = append(lines, "  - "+command)
		for index, chunk := range process.RecentChunks {
			prefix := "    \u21b3 "
			if index > 0 {
				prefix = "      "
			}
			lines = append(lines, prefix+truncateHistoryLine(chunk, width-len(prefix)))
		}
		shown++
	}
	if remaining := len(c.Processes) - shown; remaining > 0 {
		lines = append(lines, "  - ... and "+tui.FormatInt(int64(remaining))+" more running")
	}
	return lines
}

func (c UnifiedExecProcessesCell) RawLines() []string {
	return c.DisplayLines(1 << 15)
}

func firstLine(text string) string {
	for i, r := range text {
		if r == '\n' || r == '\r' {
			return text[:i]
		}
	}
	return text
}

func truncateHistoryLine(text string, width int) string {
	if width <= 0 {
		return ""
	}
	if tui.DisplayWidth(text) <= width {
		return text
	}
	if width <= 6 {
		return tui.TruncateWithEllipsis(text, width)
	}
	return tui.TruncateToWidth(text, width-6) + " [...]"
}
