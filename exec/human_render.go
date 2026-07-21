package exec

import (
	"fmt"
	"io"
	"strings"

	"codex_go/cli"
	"codex_go/protocol"
)

// execHumanRenderer mirrors Rust's EventProcessorWithHumanOutput: it renders
// thread items to stderr as they start/complete when exec is not running in
// --json mode. Colorization is controlled by the --color flag (always/never/auto).
type execHumanRenderer struct {
	stderr   io.Writer
	withAnsi bool
}

func newExecHumanRenderer(stderr io.Writer, colorFlag string) *execHumanRenderer {
	return &execHumanRenderer{
		stderr:   stderr,
		withAnsi: execHumanRendererWithAnsi(colorFlag, stderr),
	}
}

func execHumanRendererWithAnsi(colorFlag string, stderr io.Writer) bool {
	switch strings.ToLower(strings.TrimSpace(colorFlag)) {
	case "always":
		return true
	case "never":
		return false
	default:
		return isTerminalWriter(stderr)
	}
}

const (
	ansiReset   = "\x1b[0m"
	ansiBold    = "\x1b[1m"
	ansiDim     = "\x1b[2m"
	ansiItalic  = "\x1b[3m"
	ansiRed     = "\x1b[31m"
	ansiGreen   = "\x1b[32m"
	ansiYellow  = "\x1b[33m"
	ansiMagenta = "\x1b[35m"
	ansiCyan    = "\x1b[36m"
)

func (h *execHumanRenderer) style(codes string, text string) string {
	if h == nil || !h.withAnsi || text == "" {
		return text
	}
	return codes + text + ansiReset
}

func (h *execHumanRenderer) HandleEvent(event protocol.ThreadEvent) {
	if h == nil || h.stderr == nil {
		return
	}
	switch event.Type {
	case "item.started":
		if event.Item != nil {
			h.renderItemStarted(*event.Item)
		}
	case "item.completed":
		if event.Item != nil {
			h.renderItemCompleted(*event.Item)
		}
	case "error":
		if event.Error != nil && event.Error.Message != "" {
			fmt.Fprintln(h.stderr, execHumanErrorLine(h.withAnsi, event.Error.Message))
		}
	}
}

func (h *execHumanRenderer) renderItemStarted(item protocol.ThreadItem) {
	switch item.Type {
	case "command_execution":
		fmt.Fprintf(h.stderr, "%s\n%s\n", h.style(ansiItalic+ansiMagenta, "exec"), h.style(ansiBold, item.Command))
	case "mcp_tool_call":
		fmt.Fprintf(h.stderr, "%s %s %s\n",
			h.style(ansiBold, "mcp:"),
			h.style(ansiCyan, item.Server+"/"+item.Tool),
			h.style(ansiDim, "started"))
	case "web_search":
		fmt.Fprintf(h.stderr, "%s %s\n", h.style(ansiBold, "web search:"), item.Query)
	case "file_change":
		fmt.Fprintf(h.stderr, "%s\n", h.style(ansiBold, "apply patch"))
	case "collab_tool_call":
		fmt.Fprintf(h.stderr, "%s %s\n", h.style(ansiBold, "collab:"), item.Tool)
	}
}

func (h *execHumanRenderer) renderItemCompleted(item protocol.ThreadItem) {
	switch item.Type {
	case "command_execution":
		h.renderCommandExecutionCompleted(item)
	case "file_change":
		h.renderFileChangeCompleted(item)
	case "mcp_tool_call":
		h.renderMCPToolCallCompleted(item)
	case "web_search":
		fmt.Fprintf(h.stderr, "%s %s\n", h.style(ansiBold, "web search:"), item.Query)
	}
}

func (h *execHumanRenderer) renderCommandExecutionCompleted(item protocol.ThreadItem) {
	var line string
	switch item.Status {
	case "completed":
		line = h.style(ansiGreen, " succeeded:")
	case "failed":
		exitCode := 1
		if item.ExitCode != nil {
			exitCode = *item.ExitCode
		}
		line = h.style(ansiRed, fmt.Sprintf(" exited %d:", exitCode))
	default:
		line = h.style(ansiDim, " in progress:")
	}
	fmt.Fprintln(h.stderr, line)
	if item.AggregatedOutput != nil && strings.TrimSpace(*item.AggregatedOutput) != "" {
		fmt.Fprintln(h.stderr, *item.AggregatedOutput)
	}
}

func (h *execHumanRenderer) renderFileChangeCompleted(item protocol.ThreadItem) {
	statusText := "in_progress"
	switch item.Status {
	case "completed":
		statusText = "completed"
	case "failed":
		statusText = "failed"
	}
	fmt.Fprintf(h.stderr, "%s %s\n", h.style(ansiBold, "patch:"), statusText)
	for _, change := range item.Changes {
		fmt.Fprintln(h.stderr, h.style(ansiDim, change.Path))
	}
}

func (h *execHumanRenderer) renderMCPToolCallCompleted(item protocol.ThreadItem) {
	var statusText string
	switch item.Status {
	case "completed":
		statusText = h.style(ansiGreen, "completed")
	case "failed":
		statusText = h.style(ansiRed, "failed")
	default:
		statusText = h.style(ansiDim, "in_progress")
	}
	fmt.Fprintf(h.stderr, "%s %s %s\n",
		h.style(ansiBold, "mcp:"),
		h.style(ansiCyan, item.Server+"/"+item.Tool),
		h.style(ansiDim, "("+statusText+")"))
	if item.CallError != nil && item.CallError.Message != "" {
		fmt.Fprintln(h.stderr, h.style(ansiRed, item.CallError.Message))
	}
}

// execHumanErrorLine matches Rust's "ERROR: <message>" rendering for error events.
func execHumanErrorLine(withAnsi bool, message string) string {
	prefix := "ERROR:"
	if withAnsi {
		prefix = ansiRed + ansiBold + prefix + ansiReset
	}
	return prefix + " " + message
}

func execColorFlagValue(opts cli.ExecOptions) string {
	if strings.TrimSpace(opts.Color) == "" {
		return "auto"
	}
	return opts.Color
}
