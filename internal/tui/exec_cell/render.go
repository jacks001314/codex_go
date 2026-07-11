package execcell

import (
	"strings"
	"time"

	"codex_go/internal/shell"
	"codex_go/internal/tui"
	"codex_go/internal/utils"
)

// Rust parity: codex-rs/tui/src/exec_cell/render.rs.

const (
	ToolCallMaxLines          = 5
	UserShellToolCallMaxLines = 50
	maxInteractionPreview     = 80
	commandContinuationLines  = 2
	transcriptHint            = "ctrl + t to view transcript"
	ansiReset                 = "\x1b[0m"
	ansiBold                  = "\x1b[1m"
	ansiDim                   = "\x1b[2m"
	ansiGreenBold             = "\x1b[1;32m"
	ansiRedBold               = "\x1b[1;31m"
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
	sourceLines := splitCommandOutputLines(output.AggregatedOutput)
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

func (c ExecCell) DisplayLinesWithTheme(width int, themeID string) []string {
	if strings.TrimSpace(themeID) == "" {
		return c.DisplayLines(width)
	}
	if c.IsExploringCell() {
		return c.exploringDisplayLines(width)
	}
	return c.commandDisplayLinesWithTheme(width, themeID)
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
			out = append(out, splitCommandOutputLines(call.Output.FormattedOutput)...)
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
	return c.commandDisplayLinesStyled(width, "", false)
}

func (c ExecCell) commandDisplayLinesWithTheme(width int, themeID string) []string {
	return c.commandDisplayLinesStyled(width, themeID, true)
}

func (c ExecCell) commandDisplayLinesStyled(width int, themeID string, styled bool) []string {
	if len(c.Calls) != 1 {
		return nil
	}
	width = max(width, 1)
	call := c.Calls[0]
	success := call.Output != nil && call.Output.ExitCode == 0
	failed := call.Output != nil && call.Output.ExitCode != 0
	bullet := "•"
	if styled {
		switch {
		case failed:
			bullet = ansiWrap(ansiRedBold, bullet)
		case success:
			bullet = ansiWrap(ansiGreenBold, bullet)
		default:
			bullet = ansiWrap(ansiDim, bullet)
		}
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
	headerPrefix := bullet + " "
	if title != "" {
		if styled {
			headerPrefix += ansiWrap(ansiBold, title) + " "
		} else {
			headerPrefix += title + " "
		}
	}
	lines := commandHeaderLines(headerPrefix, cmdDisplay, width, themeID, styled)
	if call.Output != nil {
		limit := ToolCallMaxLines
		displayLimit := ToolCallMaxLines
		if call.IsUserShellCommand() {
			limit = UserShellToolCallMaxLines
			displayLimit = UserShellToolCallMaxLines
		}
		output := OutputLinesFor(call.Output, OutputLinesParams{LineLimit: limit})
		if len(output.Lines) == 0 && !call.IsUnifiedExecInteraction() {
			line := "  └ (no output)"
			if styled {
				line = ansiWrap(ansiDim, line)
			}
			lines = append(lines, line)
		} else {
			wrappedOutput := make([]string, 0, len(output.Lines))
			for _, line := range output.Lines {
				wrappedOutput = append(wrappedOutput, tui.AdaptiveWrapLine(line, tui.WrapOptions{
					Width:      max(width-4, 1),
					BreakWords: true,
				})...)
			}
			prefixedOutput := make([]string, 0, len(wrappedOutput))
			for index, line := range wrappedOutput {
				prefix := "    "
				if index == 0 {
					prefix = "  └ "
				}
				prefixedOutput = append(prefixedOutput, prefix+line)
			}
			for _, line := range truncateOutputLinesMiddle(prefixedOutput, displayLimit, width, output.Omitted) {
				if styled {
					line = ansiWrap(ansiDim, line)
				}
				lines = append(lines, line)
			}
		}
	}
	return lines
}

func commandHeaderLines(prefix string, command string, width int, themeID string, styled bool) []string {
	if width <= 0 {
		width = 1
	}
	prefixWidth := tui.DisplayWidth(utils.StripANSI(prefix))
	initialIndent := strings.Repeat(" ", min(prefixWidth, max(width-1, 0)))
	wrapped := tui.AdaptiveWrapLine(command, tui.WrapOptions{
		Width:            width,
		InitialIndent:    initialIndent,
		SubsequentIndent: "    ",
		BreakWords:       true,
	})
	if len(wrapped) == 0 {
		wrapped = []string{initialIndent}
	}
	first := strings.TrimPrefix(wrapped[0], initialIndent)
	lines := []string{prefix + highlightCommandLine(first, themeID, styled)}
	continuation := make([]string, 0, len(wrapped)-1)
	for _, line := range wrapped[1:] {
		continuation = append(continuation, strings.TrimPrefix(line, "    "))
	}
	if len(continuation) > commandContinuationLines {
		omitted := len(continuation) - commandContinuationLines
		continuation = append(append([]string(nil), continuation[:commandContinuationLines]...), "… +"+tui.FormatInt(int64(omitted))+" lines")
	}
	for _, line := range continuation {
		continuationPrefix := "  │ "
		if styled {
			continuationPrefix = ansiWrap(ansiDim, continuationPrefix)
		}
		lines = append(lines, continuationPrefix+highlightCommandLine(line, themeID, styled))
	}
	return lines
}

func highlightCommandLine(line string, themeID string, styled bool) string {
	if !styled {
		return line
	}
	return tui.HighlightBashANSI(line, themeID)
}

func truncateOutputLinesMiddle(lines []string, maxRows int, width int, omittedHint *int) []string {
	if maxRows <= 0 || len(lines) == 0 {
		return nil
	}
	width = max(width, 1)
	rows := make([]int, len(lines))
	totalRows := 0
	for index, line := range lines {
		lineWidth := tui.DisplayWidth(utils.StripANSI(line))
		rows[index] = max(1, (lineWidth+width-1)/width)
		totalRows += rows[index]
	}
	if totalRows <= maxRows {
		return lines
	}
	baseOmitted := 0
	if omittedHint != nil {
		baseOmitted = *omittedHint
	}
	estimatedOmitted := baseOmitted + len(lines)
	if omittedHint != nil {
		estimatedOmitted--
	}
	ellipsis := "    " + OutputEllipsisText(estimatedOmitted)
	ellipsisRows := max(1, (tui.DisplayWidth(ellipsis)+width-1)/width)
	if ellipsisRows >= maxRows {
		return []string{ellipsis}
	}

	availableRows := maxRows - ellipsisRows
	headBudget := availableRows / 2
	tailBudget := availableRows - headBudget
	headEnd := 0
	headRows := 0
	for headEnd < len(lines) && headRows+rows[headEnd] <= headBudget {
		headRows += rows[headEnd]
		headEnd++
	}
	tailStart := len(lines)
	tailRows := 0
	for tailStart > headEnd && tailRows+rows[tailStart-1] <= tailBudget {
		tailStart--
		tailRows += rows[tailStart]
	}
	additional := len(lines) - headEnd - (len(lines) - tailStart)
	if omittedHint != nil && additional > 0 {
		additional--
	}
	ellipsis = "    " + OutputEllipsisText(baseOmitted+additional)
	out := append([]string(nil), lines[:headEnd]...)
	out = append(out, ellipsis)
	out = append(out, lines[tailStart:]...)
	return out
}

func ansiWrap(code string, text string) string {
	if text == "" {
		return ""
	}
	return code + text + ansiReset
}

func FormatUnifiedExecInteraction(command []string, input string) string {
	commandDisplay := formatUnifiedExecInteractionCommand(command)
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
	return shell.StripShellCommandAndEscape(command)
}

func formatUnifiedExecInteractionCommand(command []string) string {
	if _, script, ok := shell.ExtractPOSIXShellCommand(command); ok {
		return script
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

func splitCommandOutputLines(output string) []string {
	output = strings.TrimRight(output, "\r\n")
	if output == "" {
		return nil
	}
	lines := strings.Split(output, "\n")
	for index := range lines {
		lines[index] = strings.TrimSuffix(lines[index], "\r")
	}
	return lines
}
