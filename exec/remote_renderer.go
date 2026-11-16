package exec

import (
	"fmt"
	"io"
	"strings"

	"codex_go/protocol"
)

// RemoteTurnRenderer mirrors the exec event processors for a turn driven by a
// remote app-server (Rust #61707 exec/src/remote.rs reuses the same
// EventProcessor implementations as the local path). It owns the human and
// JSONL output shapes, the thread id line, and the last-message file.
type RemoteTurnRenderer struct {
	sink            *execEventSink
	stdout          io.Writer
	stderr          io.Writer
	jsonOutput      bool
	lastMessageFile string
	finalMessage    string
	hasFinalMessage bool
	// emitFinalMessageOnShutdown mirrors Rust's flag of the same name: a
	// completed turn writes the last-message file even when the turn produced
	// no agent message, while a failed or interrupted turn writes nothing.
	emitFinalMessageOnShutdown bool
	tokenUsage                 protocol.Usage
	hasTokenUsage              bool
	planRunning                bool
	planItems                  []protocol.TodoItem
}

// remoteTodoListRawID keeps the running plan's exec item id stable across
// item.updated events; the shared sink maps it to the numbered JSON id.
const remoteTodoListRawID = "todo_list"

// NewRemoteTurnRenderer builds the renderer for `codex exec --remote`.
func NewRemoteTurnRenderer(stdout, stderr io.Writer, jsonOutput bool, color string, lastMessageFile string) *RemoteTurnRenderer {
	sink := newExecEventSink(stdout, jsonOutput)
	if !jsonOutput && stderr != nil {
		// Rust's EventProcessorWithHumanOutput::create_with_ansi with no config
		// shows reasoning summaries and never raw reasoning.
		sink.human = newExecHumanRendererWithReasoning(stderr, color, true, false)
	}
	return &RemoteTurnRenderer{
		sink:            sink,
		stdout:          stdout,
		stderr:          stderr,
		jsonOutput:      jsonOutput,
		lastMessageFile: lastMessageFile,
	}
}

// PrintThreadStarted emits the human `thread id: <id>` line or the JSON
// thread.started event (Rust EventProcessor::print_thread_started).
func (r *RemoteTurnRenderer) PrintThreadStarted(threadID string) error {
	if r == nil {
		return nil
	}
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return nil
	}
	if r.jsonOutput {
		return r.sink.Emit(protocol.ThreadStarted(threadID))
	}
	if r.stderr != nil {
		fmt.Fprintf(r.stderr, "thread id: %s\n", threadID)
	}
	return nil
}

// Emit renders one exec protocol event through the shared event sink.
func (r *RemoteTurnRenderer) Emit(event protocol.ThreadEvent) error {
	if r == nil {
		return nil
	}
	return r.sink.Emit(event)
}

// PrintWarning renders a server warning: a human warning line, or the
// item.completed error item the JSONL contract uses for warnings.
func (r *RemoteTurnRenderer) PrintWarning(message string) error {
	if r == nil {
		return nil
	}
	message = strings.TrimSpace(message)
	if message == "" {
		return nil
	}
	if r.jsonOutput {
		return r.sink.Emit(protocol.ItemCompleted(protocol.ErrorItem("", message)))
	}
	if r.stderr != nil {
		fmt.Fprintf(r.stderr, "warning: %s\n", message)
	}
	return nil
}

// PrintDeprecation renders a deprecation notice.
func (r *RemoteTurnRenderer) PrintDeprecation(summary string, details *string) error {
	if r == nil {
		return nil
	}
	summary = strings.TrimSpace(summary)
	detailText := ""
	if details != nil {
		detailText = strings.TrimSpace(*details)
	}
	if r.jsonOutput {
		message := summary
		if detailText != "" {
			message = fmt.Sprintf("%s (%s)", summary, detailText)
		}
		return r.sink.Emit(protocol.ItemCompleted(protocol.ErrorItem("", message)))
	}
	if r.stderr != nil {
		fmt.Fprintf(r.stderr, "deprecated: %s\n", summary)
		if detailText != "" {
			fmt.Fprintln(r.stderr, detailText)
		}
	}
	return nil
}

// PrintError renders a turn error notification. The JSONL contract appends the
// error's additional details, while the human line prints TurnError's Display
// message alone. The caller still decides the process exit status.
func (r *RemoteTurnRenderer) PrintError(message, details string) error {
	if r == nil {
		return nil
	}
	message = strings.TrimSpace(message)
	if message == "" {
		return nil
	}
	if r.jsonOutput {
		full := message
		if details = strings.TrimSpace(details); details != "" {
			full = fmt.Sprintf("%s (%s)", message, details)
		}
		return r.sink.Emit(protocol.ErrorEvent(full))
	}
	if r.stderr != nil {
		fmt.Fprintf(r.stderr, "ERROR: %s\n", message)
	}
	return nil
}

// PrintTurnFailed renders a failed turn. The JSONL processor always emits
// turn.failed (with details), while the human processor prints ERROR only when
// the turn carried its own error.
func (r *RemoteTurnRenderer) PrintTurnFailed(jsonMessage, humanMessage string) error {
	if r == nil {
		return nil
	}
	if r.jsonOutput {
		return r.sink.Emit(protocol.TurnFailed(strings.TrimSpace(jsonMessage)))
	}
	if humanMessage = strings.TrimSpace(humanMessage); humanMessage == "" {
		return nil
	}
	if r.stderr != nil {
		fmt.Fprintf(r.stderr, "ERROR: %s\n", humanMessage)
	}
	return nil
}

// PrintInterrupted prints the human "turn interrupted" line; the JSONL
// processor emits nothing for an interrupted turn.
func (r *RemoteTurnRenderer) PrintInterrupted() {
	if r == nil || r.jsonOutput || r.stderr == nil {
		return
	}
	label := "turn interrupted"
	if human := r.sink.human; human != nil {
		label = human.style(ansiDim, label)
	}
	fmt.Fprintln(r.stderr, label)
}

// PrintModelReroute renders a model reroute notice (Rust's exec processors
// print a human line and a JSON error item).
func (r *RemoteTurnRenderer) PrintModelReroute(fromModel, toModel, reason string) error {
	if r == nil {
		return nil
	}
	fromModel = strings.TrimSpace(fromModel)
	toModel = strings.TrimSpace(toModel)
	if r.jsonOutput {
		message := fmt.Sprintf("model rerouted: %s -> %s", fromModel, toModel)
		// Rust formats the reroute reason with `{:?}`, which prints the
		// PascalCase variant of ModelRerouteReason.
		displayReason := rustStyleModelRerouteReason(reason)
		if displayReason == "" {
			displayReason = strings.TrimSpace(reason)
		}
		if displayReason != "" {
			message = fmt.Sprintf("%s (%s)", message, displayReason)
		}
		return r.sink.Emit(protocol.ItemCompleted(protocol.ErrorItem("", message)))
	}
	if r.stderr != nil {
		fmt.Fprintf(r.stderr, "model rerouted: %s -> %s\n", fromModel, toModel)
	}
	return nil
}

// PrintAuthRecovery renders a provider auth-recovery message in human mode.
// Rust prints the completion message in green.
func (r *RemoteTurnRenderer) PrintAuthRecovery(message string, completed bool) {
	if r == nil || r.jsonOutput || r.stderr == nil {
		return
	}
	if message = strings.TrimSpace(message); message != "" {
		if completed && r.sink.human != nil {
			message = r.sink.human.style(ansiGreen, message)
		}
		fmt.Fprintln(r.stderr, message)
	}
}

// PrintHook renders a hook lifecycle line in human mode (Rust prints the hook
// event name and, on completion, its status).
func (r *RemoteTurnRenderer) PrintHook(name, status string) {
	if r == nil || r.jsonOutput || r.stderr == nil {
		return
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return
	}
	if status = strings.TrimSpace(status); status == "" {
		fmt.Fprintf(r.stderr, "hook: %s\n", name)
		return
	}
	fmt.Fprintf(r.stderr, "hook: %s %s\n", name, status)
}

// SetFinalMessage records the turn's final agent message for the last-message
// file.
func (r *RemoteTurnRenderer) SetFinalMessage(message string) {
	if r == nil {
		return
	}
	r.finalMessage = message
	r.hasFinalMessage = true
	r.emitFinalMessageOnShutdown = true
}

// ClearFinalMessage mirrors Rust's failed/interrupted turn handling: the turn
// produced no final answer, so no last-message file is written.
func (r *RemoteTurnRenderer) ClearFinalMessage() {
	if r == nil {
		return
	}
	r.finalMessage = ""
	r.hasFinalMessage = false
	r.emitFinalMessageOnShutdown = false
}

// SetTokenUsage records the latest thread token usage so the human summary can
// print Rust's "tokens used" line at shutdown.
func (r *RemoteTurnRenderer) SetTokenUsage(usage protocol.Usage) {
	if r == nil {
		return
	}
	r.tokenUsage = usage
	r.hasTokenUsage = true
}

// EmitPlan mirrors Rust's TurnPlanUpdated arm: the first update starts a
// todo_list item, later updates refresh it, and the completion is flushed when
// the turn ends.
func (r *RemoteTurnRenderer) EmitPlan(items []protocol.TodoItem) error {
	if r == nil {
		return nil
	}
	r.planItems = append([]protocol.TodoItem(nil), items...)
	if r.planRunning {
		return r.sink.Emit(protocol.ItemUpdated(protocol.TodoListItem(remoteTodoListRawID, items)))
	}
	r.planRunning = true
	return r.sink.Emit(protocol.ItemStarted(protocol.TodoListItem(remoteTodoListRawID, items)))
}

// FlushPlan completes a running todo list, matching the item.completed event
// Rust emits when the turn finishes in any status.
func (r *RemoteTurnRenderer) FlushPlan() error {
	if r == nil || !r.planRunning {
		return nil
	}
	r.planRunning = false
	return r.sink.Emit(protocol.ItemCompleted(protocol.TodoListItem(remoteTodoListRawID, r.planItems)))
}

// Finish mirrors the tail of Rust's print_final_output: write the last-message
// file, print the human token summary, then emit the final message on stdout
// when the streams are not both terminals.
func (r *RemoteTurnRenderer) Finish() error {
	if r == nil {
		return nil
	}
	if strings.TrimSpace(r.lastMessageFile) != "" && r.emitFinalMessageOnShutdown {
		writeLastMessageFile(r.stderr, r.lastMessageFile, r.finalMessage, r.hasFinalMessage)
	}
	if r.jsonOutput {
		return nil
	}
	if r.hasTokenUsage {
		if total := blendedRemoteTokenTotal(r.tokenUsage); total > 0 && r.stderr != nil {
			fmt.Fprintf(r.stderr, "tokens used\n%s\n", formatIntWithSeparators(total))
		}
	}
	if !r.hasFinalMessage {
		return nil
	}
	label := "codex"
	rendered := false
	if human := r.sink.human; human != nil {
		label = human.style(ansiItalic+ansiMagenta, "codex")
		rendered = human.agentMessageRendered && human.lastAgentMessage == r.finalMessage
	}
	return writeHumanFinalMessage(r.stdout, r.stderr, r.finalMessage, rendered, label)
}

func blendedRemoteTokenTotal(usage protocol.Usage) int64 {
	cachedInput := usage.CachedInputTokens
	if cachedInput < 0 {
		cachedInput = 0
	}
	nonCachedInput := usage.InputTokens - cachedInput
	if nonCachedInput < 0 {
		nonCachedInput = 0
	}
	output := usage.OutputTokens
	if output < 0 {
		output = 0
	}
	return nonCachedInput + output
}
