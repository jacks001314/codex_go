package execcell

import (
	"strings"
	"time"

	"codex_go/internal/tui"
)

// Rust parity: codex-rs/tui/src/exec_cell/render.rs.

const (
	ToolCallMaxLines          = 5
	UserShellToolCallMaxLines = 50
	maxInteractionPreview     = 80
	transcriptHint            = "ctrl + t to view transcript"
)

type OutputLinesParams struct {
	LineLimit        int
	OnlyErr          bool
	IncludeAnglePipe bool
	IncludePrefix    bool
}

type OutputLines struct {
	Lines   []string
	Omitted *int
}

func NewActiveExecCommand(callID string, command []string, parsed []ParsedCommand, source ExecCommandSource, interactionInput string, animationsEnabled bool) ExecCell {
	now := time.Now()
	return NewExecCell(ExecCall{
		CallID:           callID,
		Command:          append([]string(nil), command...),
		Parsed:           append([]ParsedCommand(nil), parsed...),
		Source:           source,
		StartTime:        &now,
		InteractionInput: interactionInput,
	}, animationsEnabled)
}

func OutputLinesFor(output *CommandOutput, params OutputLinesParams) OutputLines {
	if output == nil || (params.OnlyErr && output.ExitCode == 0) {
		return OutputLines{}
	}
	lineLimit := params.LineLimit
	if lineLimit <= 0 {
		lineLimit = ToolCallMaxLines
	}
	sourceLines := strings.Split(strings.TrimRight(output.AggregatedOutput, "\n"), "\n")
	if len(sourceLines) == 1 && sourceLines[0] == "" {
		sourceLines = nil
	}
	total := len(sourceLines)
	headEnd := min(total, lineLimit)
	out := []string{}
	for i, raw := range sourceLines[:headEnd] {
		out = append(out, outputPrefix(i, params)+raw)
	}
	showEllipsis := total > 2*lineLimit
	var omitted *int
	if showEllipsis {
		value := total - 2*lineLimit
		omitted = &value
		out = append(out, OutputEllipsisText(value))
	}
	tailStart := headEnd
	if showEllipsis {
		tailStart = total - lineLimit
	}
	for _, raw := range sourceLines[tailStart:] {
		prefix := ""
		if params.IncludePrefix {
			prefix = "    "
		}
		out = append(out, prefix+raw)
	}
	return OutputLines{Lines: out, Omitted: omitted}
}

func (c ExecCell) DisplayLines(width int) []string {
	if c.IsExploringCell() {
		return c.exploringDisplayLines(width)
	}
	return c.commandDisplayLines(width)
}

func (c ExecCell) TranscriptLines(width int) []string {
	out := []string{}
	for index, call := range c.Calls {
		if index > 0 {
			out = append(out, "")
		}
		script := StripShellCommand(call.Command)
		out = append(out, tui.AdaptiveWrapLine(script, tui.WrapOptions{
			Width:            width,
			InitialIndent:    "$ ",
			SubsequentIndent: "    ",
			BreakWords:       true,
		})...)
		if call.Output != nil && !call.IsUnifiedExecInteraction() {
			out = append(out, strings.Split(strings.TrimRight(call.Output.FormattedOutput, "\n"), "\n")...)
		}
		if call.Output != nil {
			result := "✓"
			if call.Output.ExitCode != 0 {
				result += " (" + tui.FormatInt(int64(call.Output.ExitCode)) + ")"
			}
			if call.Duration != nil {
				result += " - " + call.Duration.String()
			}
			out = append(out, result)
		}
	}
	return out
}

func (c ExecCell) RawLines() []string {
	return c.TranscriptLines(1 << 15)
}

func (c ExecCell) exploringDisplayLines(width int) []string {
	title := "Explored"
	if c.IsActive() {
		title = "Exploring"
	}
	out := []string{"• " + title}
	for _, call := range c.Calls {
		for _, parsed := range call.Parsed {
			label, body := parsedDisplay(parsed)
			wrapped := tui.AdaptiveWrapLine(body, tui.WrapOptions{
				Width:            width,
				InitialIndent:    "  └ " + label + " ",
				SubsequentIndent: "    ",
				BreakWords:       true,
			})
			out = append(out, wrapped...)
		}
	}
	return out
}

func (c ExecCell) commandDisplayLines(width int) []string {
	if len(c.Calls) != 1 {
		return nil
	}
	call := c.Calls[0]
	success := call.Output != nil && call.Output.ExitCode == 0
	failed := call.Output != nil && call.Output.ExitCode != 0
	bullet := "•"
	if failed {
		bullet = "×"
	} else if success {
		bullet = "✓"
	}
	title := "Running"
	if call.Output != nil {
		if call.IsUserShellCommand() {
			title = "You ran"
		} else {
			title = "Ran"
		}
	}
	cmdDisplay := StripShellCommand(call.Command)
	if call.IsUnifiedExecInteraction() {
		cmdDisplay = FormatUnifiedExecInteraction(call.Command, call.InteractionInput)
		title = ""
	}
	header := strings.TrimSpace(bullet + " " + title + " " + cmdDisplay)
	lines := tui.AdaptiveWrapLine(header, tui.WrapOptions{Width: width, BreakWords: true})
	if call.Output != nil {
		limit := ToolCallMaxLines
		if call.IsUserShellCommand() {
			limit = UserShellToolCallMaxLines
		}
		output := OutputLinesFor(call.Output, OutputLinesParams{LineLimit: limit})
		if len(output.Lines) == 0 && !call.IsUnifiedExecInteraction() {
			lines = append(lines, "  └ (no output)")
		} else {
			for _, line := range output.Lines {
				lines = append(lines, tui.AdaptiveWrapLine(line, tui.WrapOptions{
					Width:            width,
					InitialIndent:    "  └ ",
					SubsequentIndent: "    ",
					BreakWords:       true,
				})...)
			}
		}
	}
	return lines
}

func FormatUnifiedExecInteraction(command []string, input string) string {
	commandDisplay := StripShellCommand(command)
	if input != "" {
		return "Interacted with `" + commandDisplay + "`, sent `" + SummarizeInteractionInput(input) + "`"
	}
	return "Waited for `" + commandDisplay + "`"
}

func SummarizeInteractionInput(input string) string {
	singleLine := strings.ReplaceAll(input, "\n", "\\n")
	sanitized := strings.ReplaceAll(singleLine, "`", "\\`")
	runes := []rune(sanitized)
	if len(runes) <= maxInteractionPreview {
		return sanitized
	}
	return string(runes[:maxInteractionPreview]) + "..."
}

func OutputEllipsisText(omitted int) string {
	return "… +" + tui.FormatInt(int64(omitted)) + " lines (" + transcriptHint + ")"
}

func StripShellCommand(command []string) string {
	if len(command) >= 3 && (strings.HasSuffix(command[0], "bash") || strings.HasSuffix(command[0], "sh")) && command[1] == "-lc" {
		return command[2]
	}
	return strings.Join(command, " ")
}

func parsedDisplay(parsed ParsedCommand) (string, string) {
	switch parsed.Kind {
	case ParsedRead:
		return "Read", parsed.Name
	case ParsedListFiles:
		if parsed.Path != "" {
			return "List", parsed.Path
		}
		return "List", parsed.Cmd
	case ParsedSearch:
		if parsed.Query != "" && parsed.Path != "" {
			return "Search", parsed.Query + " in " + parsed.Path
		}
		if parsed.Query != "" {
			return "Search", parsed.Query
		}
		return "Search", parsed.Cmd
	default:
		return "Run", parsed.Cmd
	}
}

func outputPrefix(index int, params OutputLinesParams) string {
	if !params.IncludePrefix {
		return ""
	}
	if index == 0 && params.IncludeAnglePipe {
		return "  └ "
	}
	return "    "
}
