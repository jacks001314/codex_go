package tea

import (
	"strconv"
	"strings"
	"time"

	"codex_go/protocol"
	"codex_go/shell"
	codextui "codex_go/tui"
	execcell "codex_go/tui/exec_cell"
	historycell "codex_go/tui/history_cell"
)

// Rust parity: codex-rs/tui/src/thread_transcript (tools.rs, other_items.rs).
//
// ResumeTranscriptBuilder projects persisted thread items onto the same rich
// history cells the live event path produces (Rust #46710). These values carry
// presentation data only: projecting a historical item never starts a task,
// changes composer state, or leaves an animation running, and an unfinished
// item keeps its last known status.
type ResumeTranscriptBuilder struct {
	cwd     string
	width   int
	pending *execcell.ExecCell
}

// NewResumeTranscriptBuilder creates a projector for one loaded transcript.
func NewResumeTranscriptBuilder(cwd string, width int) *ResumeTranscriptBuilder {
	if width <= 0 {
		width = 100
	}
	return &ResumeTranscriptBuilder{cwd: strings.TrimSpace(cwd), width: width}
}

// Project returns the history messages a persisted item contributes. The
// boolean reports that the projector owns the item type; a nil slice with true
// means the item intentionally renders nothing (for example an empty completed
// patch).
func (b *ResumeTranscriptBuilder) Project(item *protocol.ThreadItem) ([]codextui.Message, bool) {
	if b == nil || item == nil {
		return nil, false
	}
	switch normalizeResumeItemType(item.Type) {
	case "commandexecution":
		return b.projectCommand(*item), true
	case "mcptoolcall":
		return b.projectMCP(*item), true
	case "filechange":
		return b.projectFileChange(*item), true
	case "dynamictoolcall":
		return b.projectDynamicTool(*item), true
	case "collabagenttoolcall":
		return b.projectCollab(*item), true
	case "subagentactivity":
		return b.projectSubAgentActivity(*item), true
	case "websearch":
		return b.projectWebSearch(*item), true
	case "imageview":
		return b.projectImageView(*item), true
	case "imagegeneration":
		return b.projectImageGeneration(*item), true
	case "hookprompt":
		return b.projectHookPrompt(*item), true
	}
	return nil, false
}

// AttachReasoning adds a transcript-only reasoning block to the open
// exploration group, so adjacent commands keep grouping across hidden reasoning
// (Rust thread_items_to_transcript_cells).
func (b *ResumeTranscriptBuilder) AttachReasoning(itemID string, content string, rawContent string) bool {
	if b == nil || b.pending == nil {
		return false
	}
	return b.pending.AppendReasoning(itemID, content, rawContent)
}

// Flush commits the open exploration group, if any.
func (b *ResumeTranscriptBuilder) Flush() []codextui.Message {
	if b == nil || b.pending == nil {
		return nil
	}
	cell := *b.pending
	b.pending = nil
	return []codextui.Message{b.restoredMessage(cell)}
}

// restoredMessage turns a cell into a history message and filters untrusted
// control characters from the stored text (Rust #46710).
func (b *ResumeTranscriptBuilder) restoredMessage(cell historycell.HistoryCell) codextui.Message {
	message := historyMessageFromCell(cell, b.width)
	message.Text = codextui.SanitizeHistoryText(message.Text)
	message.RawText = codextui.SanitizeHistoryText(message.RawText)
	message.TranscriptText = codextui.SanitizeHistoryText(message.TranscriptText)
	if execCell, ok := cell.(execcell.ExecCell); ok && len(execCell.Reasoning) > 0 {
		message.TranscriptText = codextui.SanitizeHistoryText(strings.Join(
			execCell.TranscriptLinesWithReasoning(b.width, func(reasoning codextui.ActivityReasoning, width int) []string {
				return reasoningBlockLines(reasoning.Content, reasoning.RawContent, width, "", "", false)
			}),
			"\n",
		))
	}
	return message
}

func (b *ResumeTranscriptBuilder) flushThen(cell historycell.HistoryCell) []codextui.Message {
	messages := b.Flush()
	if cell == nil {
		return messages
	}
	return append(messages, b.restoredMessage(cell))
}

func (b *ResumeTranscriptBuilder) flushThenPlain(lines []string) []codextui.Message {
	if len(lines) == 0 {
		return b.Flush()
	}
	return b.flushThen(historycell.NewPlainHistoryCell(lines))
}

func (b *ResumeTranscriptBuilder) projectCommand(item protocol.ThreadItem) []codextui.Message {
	source := execCommandSourceForItem(&item)
	status := normalizeResumeStatus(item.Status)
	if source == execcell.ExecSourceUnifiedExecInteraction || status == "in_progress" || status == "declined" {
		// Declined commands never ran, terminal interactions do not carry stdin
		// details, and a pending command retains its last known state without a
		// running clock (Rust historical_tool_fallback).
		return b.flushThenPlain(resumeCommandFallbackLines(item, source, status))
	}
	args := resumeCommandArgs(item)
	if len(args) == 0 {
		return b.flushThenPlain(resumeCommandFallbackLines(item, source, status))
	}
	exitCode := 0
	if item.ExitCode != nil {
		exitCode = *item.ExitCode
	} else if status == "failed" {
		exitCode = 1
	}
	output := ""
	if item.AggregatedOutput != nil {
		output = *item.AggregatedOutput
	}
	duration := resumeItemDuration(item)
	call := execcell.ExecCall{
		CallID:  firstNonEmpty(strings.TrimSpace(item.CallID), strings.TrimSpace(item.ID)),
		Command: args,
		Parsed:  parsedCommandsForExecCellItem(&item),
		Source:  source,
		Output: &execcell.CommandOutput{
			ExitCode:         exitCode,
			AggregatedOutput: output,
			FormattedOutput:  output,
		},
		Duration: &duration,
	}
	if b.pending != nil {
		next, ok := b.pending.WithAddedCall(call.CallID, call.Command, call.Parsed, call.Source, "")
		if ok {
			next.CompleteCall(call.CallID, *call.Output, duration)
			b.pending = &next
		} else {
			messages := b.Flush()
			cell := execcell.NewExecCell(call, false)
			b.pending = &cell
			if cell.ShouldFlush() {
				messages = append(messages, b.Flush()...)
			}
			return messages
		}
	} else {
		cell := execcell.NewExecCell(call, false)
		b.pending = &cell
	}
	if b.pending != nil && b.pending.ShouldFlush() {
		return b.Flush()
	}
	return nil
}

func (b *ResumeTranscriptBuilder) projectMCP(item protocol.ThreadItem) []codextui.Message {
	status := normalizeResumeStatus(item.Status)
	server := strings.TrimSpace(item.Server)
	tool := strings.TrimSpace(item.Tool)
	if status == "in_progress" || (item.Result == nil && item.CallError == nil) {
		label := "In progress at last update"
		switch {
		case status == "in_progress":
		case status == "failed":
			label = "Failed \u00b7 result unavailable"
		default:
			label = "Completed \u00b7 result unavailable"
		}
		return b.flushThenPlain([]string{resumeMCPFallbackLine(server, tool, label)})
	}
	invocation := historycell.McpInvocation{Server: server, Tool: tool}
	if item.Arguments != nil {
		if arguments := compactMCPArguments(*item.Arguments); arguments != "" {
			invocation.Arguments = arguments
		}
	}
	result := historycell.McpToolResult{}
	if item.CallError != nil {
		result.Error = strings.TrimSpace(item.CallError.Message)
		result.IsError = true
	} else {
		result.Content = resumeMCPContent(item.Result.Content)
		result.IsError = status == "failed"
	}
	cell := historycell.NewMcpToolCall(
		firstNonEmpty(strings.TrimSpace(item.CallID), strings.TrimSpace(item.ID)),
		invocation,
		result,
	)
	return b.flushThen(cell)
}

func (b *ResumeTranscriptBuilder) projectFileChange(item protocol.ThreadItem) []codextui.Message {
	status := normalizeResumeStatus(item.Status)
	cellID := firstNonEmpty(strings.TrimSpace(item.CallID), strings.TrimSpace(item.ID))
	switch status {
	case "failed":
		return b.flushThen(historycell.NewPatchApplyFailure(""))
	case "in_progress", "declined":
		message := "Patch application in progress"
		if status == "declined" {
			message = "Patch application declined"
		}
		return b.flushThen(historycell.NewInfoEvent(message, ""))
	}
	changes := resumeFileChanges(item.Changes)
	if len(changes) == 0 {
		// A completed patch without retained changes renders nothing
		// (Rust other_items::cells).
		return b.Flush()
	}
	// Restored history text is stored, so it must stay free of ANSI styling;
	// the renderer applies theme colors to live cells only.
	cell := historycell.NewPatchEventWithTheme(changes, b.cwd, "").WithActivityID(cellID)
	return b.flushThen(cell)
}

func (b *ResumeTranscriptBuilder) projectDynamicTool(item protocol.ThreadItem) []codextui.Message {
	status := normalizeResumeStatus(item.Status)
	if item.Success != nil && !*item.Success {
		status = "failed"
	}
	if status == "" {
		status = "completed"
	}
	name := strings.TrimSpace(item.ToolName)
	if name == "" {
		name = strings.TrimSpace(item.Tool)
	}
	cell := historycell.DynamicToolCallCell{
		CallID:    firstNonEmpty(strings.TrimSpace(item.CallID), strings.TrimSpace(item.ID)),
		Name:      name,
		Arguments: dynamicToolArgumentsText(item.Arguments),
		Status:    status,
		Output:    resumeDynamicToolOutput(item),
	}
	if durationMS := resumeDurationMS(item); durationMS >= 0 {
		cell.DurationMS = &durationMS
	}
	return b.flushThen(cell)
}

func (b *ResumeTranscriptBuilder) projectCollab(item protocol.ThreadItem) []codextui.Message {
	completed := normalizeResumeStatus(item.Status) == "completed"
	cell, ok := historycell.NewCollabAgentToolCall(&item, completed)
	if !ok {
		status := normalizeResumeStatus(item.Status)
		if status != "completed" {
			label := resumeStatusLabel(status)
			tool := strings.TrimSpace(item.ToolName)
			if tool == "" {
				tool = "agent"
			}
			return b.flushThenPlain([]string{"agent tool: " + tool + " \u00b7 " + label})
		}
		return b.Flush()
	}
	return b.flushThen(cell)
}

func (b *ResumeTranscriptBuilder) projectSubAgentActivity(item protocol.ThreadItem) []codextui.Message {
	cell, ok := historycell.NewSubAgentActivity(item.ActivityKind, item.AgentPath)
	if !ok {
		return b.Flush()
	}
	return b.flushThen(cell)
}

func (b *ResumeTranscriptBuilder) projectWebSearch(item protocol.ThreadItem) []codextui.Message {
	id := firstNonEmpty(strings.TrimSpace(item.CallID), strings.TrimSpace(item.ID))
	return b.flushThen(historycell.NewWebSearchCall(id, item.Query, webSearchActionFromProtocolItem(&item)))
}

func (b *ResumeTranscriptBuilder) projectImageView(item protocol.ThreadItem) []codextui.Message {
	id := firstNonEmpty(strings.TrimSpace(item.CallID), strings.TrimSpace(item.ID))
	return b.flushThen(historycell.NewViewImageToolCallWithID(id, item.Path, b.cwd))
}

func (b *ResumeTranscriptBuilder) projectImageGeneration(item protocol.ThreadItem) []codextui.Message {
	id := firstNonEmpty(strings.TrimSpace(item.CallID), strings.TrimSpace(item.ID))
	status := normalizeResumeStatus(item.Status)
	if status != "completed" && status != "failed" {
		label := "status unavailable"
		if status != "" {
			label = status
		}
		return b.flushThen(historycell.NewInfoEvent("Image generation \u00b7 "+label, ""))
	}
	return b.flushThen(historycell.NewImageGenerationCall(id, status, item.RevisedPrompt, item.SavedPath))
}

func (b *ResumeTranscriptBuilder) projectHookPrompt(item protocol.ThreadItem) []codextui.Message {
	lines := []string{}
	for _, fragment := range item.Content {
		fragment = strings.TrimSpace(fragment)
		if fragment == "" {
			continue
		}
		lines = append(lines, "hook prompt: "+fragment)
	}
	if len(lines) == 0 {
		return b.Flush()
	}
	return b.flushThenPlain(lines)
}

func normalizeResumeItemType(value string) string {
	value = strings.TrimSpace(value)
	value = strings.ReplaceAll(value, "_", "")
	value = strings.ReplaceAll(value, "-", "")
	return strings.ToLower(value)
}

// normalizeResumeStatus folds the stored status spellings onto the four states
// the historical projection distinguishes. An empty status is treated as the
// last known completion, which is what a fully written item implies.
func normalizeResumeStatus(value string) string {
	switch strings.ToLower(strings.ReplaceAll(strings.TrimSpace(value), "-", "_")) {
	case "in_progress", "inprogress", "running":
		return "in_progress"
	case "failed", "error":
		return "failed"
	case "declined":
		return "declined"
	default:
		return "completed"
	}
}

func resumeStatusLabel(status string) string {
	switch status {
	case "in_progress":
		return "In progress at last update"
	case "failed":
		return "Failed"
	case "declined":
		return "Declined"
	default:
		return "Completed"
	}
}

func resumeCommandArgs(item protocol.ThreadItem) []string {
	command := strings.TrimSpace(item.Command)
	if command == "" {
		return nil
	}
	args := shell.SplitCommandLine(command)
	if len(args) == 0 {
		return nil
	}
	return args
}

func resumeCommandFallbackLines(item protocol.ThreadItem, source execcell.ExecCommandSource, status string) []string {
	label := resumeStatusLabel(status)
	statusLine := label
	if source == execcell.ExecSourceUnifiedExecInteraction {
		statusLine = "Terminal interaction \u00b7 " + label
	}
	if (status == "completed" || status == "failed") && item.ExitCode != nil {
		statusLine += " \u00b7 exit " + strconv.Itoa(*item.ExitCode)
	}
	lines := []string{"$ " + strings.TrimSpace(item.Command), statusLine}
	if item.AggregatedOutput != nil {
		output := strings.TrimRight(*item.AggregatedOutput, "\n")
		if output != "" {
			for _, line := range strings.Split(output, "\n") {
				lines = append(lines, "  "+strings.TrimRight(line, " \t"))
			}
		}
	}
	return lines
}

func resumeMCPFallbackLine(server string, tool string, label string) string {
	name := strings.TrimSpace(server)
	if name != "" && strings.TrimSpace(tool) != "" {
		name += "/" + strings.TrimSpace(tool)
	} else if name == "" {
		name = strings.TrimSpace(tool)
	}
	if name == "" {
		name = "tool"
	}
	return "mcp tool: " + name + " \u00b7 " + label
}

// resumeMCPContent keeps the textual result blocks a persisted MCP call stored.
func resumeMCPContent(content []any) []string {
	lines := make([]string, 0, len(content))
	for _, value := range content {
		switch typed := value.(type) {
		case string:
			if typed = strings.TrimSpace(typed); typed != "" {
				lines = append(lines, typed)
			}
		case map[string]any:
			if text, ok := typed["text"].(string); ok {
				if text = strings.TrimSpace(text); text != "" {
					lines = append(lines, text)
				}
			}
		}
	}
	return lines
}

// resumeDynamicToolOutput mirrors the retained output blocks of a dynamic tool
// call. A nil slice means the projection never received one.
func resumeDynamicToolOutput(item protocol.ThreadItem) []string {
	output := []string{}
	for _, block := range item.Content {
		if block = strings.TrimSpace(block); block != "" {
			output = append(output, block)
		}
	}
	if len(output) == 0 {
		for _, block := range []string{item.Output, item.Text} {
			if block = strings.TrimSpace(block); block != "" {
				output = append(output, block)
			}
		}
	}
	if len(output) == 0 && normalizeResumeStatus(item.Status) != "in_progress" {
		return nil
	}
	return output
}

func resumeDurationMS(item protocol.ThreadItem) int64 {
	for _, key := range []string{"durationMs", "duration_ms"} {
		switch value := item.Metadata[key].(type) {
		case float64:
			if value >= 0 {
				return int64(value)
			}
		case int:
			if value >= 0 {
				return int64(value)
			}
		case int64:
			if value >= 0 {
				return int64(value)
			}
		case string:
			if parsed, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64); err == nil && parsed >= 0 {
				return parsed
			}
		}
	}
	return -1
}

func resumeItemDuration(item protocol.ThreadItem) time.Duration {
	if millis := resumeDurationMS(item); millis > 0 {
		return time.Duration(millis) * time.Millisecond
	}
	return 0
}

// resumeFileChanges converts persisted app-server file changes into the display
// model, dropping the trailing "Moved to:" annotation the wire diff repeats
// next to the move path (Rust file_update_changes_to_display).
func resumeFileChanges(changes []protocol.FileChange) map[string]codextui.FileChange {
	if len(changes) == 0 {
		return nil
	}
	result := make(map[string]codextui.FileChange, len(changes))
	for _, change := range changes {
		path := strings.TrimSpace(change.Path)
		if path == "" {
			continue
		}
		diff := change.Diff
		movePath := strings.TrimSpace(change.MovePath)
		if movePath != "" {
			suffix := "\n\nMoved to: " + change.MovePath
			if strings.HasSuffix(diff, suffix) {
				diff = strings.TrimSuffix(diff, suffix)
			}
		}
		switch strings.ToLower(strings.TrimSpace(change.Kind)) {
		case "add", "added":
			result[path] = codextui.NewAddFileChange(diff)
		case "delete", "deleted":
			result[path] = codextui.NewDeleteFileChange(diff)
		default:
			result[path] = codextui.NewUpdateFileChange(diff, movePath)
		}
	}
	return result
}
