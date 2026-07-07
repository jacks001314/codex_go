package shell

import (
	"fmt"
	"strings"
	"time"

	"codex_go/internal/eventmap"
)

type ExecOutput struct {
	ExitCode int
	Duration time.Duration
	Stdout   string
	Stderr   string
}

type CommandRecord struct {
	Command  string
	ExitCode int
	Duration time.Duration
	Output   string
}

func NewCommandRecord(command string, execOutput ExecOutput, maxOutputChars int) *CommandRecord {
	output := FormatExecOutput(execOutput, maxOutputChars)
	return &CommandRecord{
		Command:  command,
		ExitCode: execOutput.ExitCode,
		Duration: execOutput.Duration,
		Output:   output,
	}
}

func (r *CommandRecord) Render() string {
	if r == nil {
		return ""
	}
	lines := []string{
		"<user_shell_command>",
		"command: " + r.Command,
		fmt.Sprintf("exit_code: %d", r.ExitCode),
	}
	if r.Duration > 0 {
		lines = append(lines, "duration: "+r.Duration.String())
	}
	if strings.TrimSpace(r.Output) != "" {
		lines = append(lines, "output:", r.Output)
	}
	lines = append(lines, "</user_shell_command>")
	return strings.Join(lines, "\n")
}

func (r *CommandRecord) ResponseItem() eventmap.ResponseItem {
	return eventmap.ResponseItem{
		Kind:    eventmap.ResponseMessage,
		Role:    "user",
		Content: []eventmap.ContentItem{{Kind: eventmap.ContentInputText, Text: r.Render()}},
	}
}

func FormatExecOutput(output ExecOutput, maxChars int) string {
	var builder strings.Builder
	if output.Stdout != "" {
		builder.WriteString(output.Stdout)
	}
	if output.Stderr != "" {
		if builder.Len() > 0 && !strings.HasSuffix(builder.String(), "\n") {
			builder.WriteByte('\n')
		}
		builder.WriteString(output.Stderr)
	}
	text := strings.TrimRight(builder.String(), "\n")
	if maxChars > 0 && len(text) > maxChars {
		return text[:maxChars] + "\n[truncated]"
	}
	return text
}
