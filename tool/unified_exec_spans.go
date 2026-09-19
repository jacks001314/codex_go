package tool

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"
)

// Rust parity: codex-rs/core/src/unified_exec/mod.rs's `trace_id` and the
// lifecycle spans of #45505 (oneshot.rs, process_manager.rs).

// MaxUnifiedExecTraceIDBytes caps the id fields Rust records on a unified-exec
// span: an id longer than this is omitted rather than truncated.
const MaxUnifiedExecTraceIDBytes = 256

// UnifiedExec span names (Rust #45505).
const (
	// UnifiedExecExecCommandSpanName brackets one exec_command call, in either
	// mode.
	UnifiedExecExecCommandSpanName = "unified_exec.exec_command"
	// UnifiedExecWriteStdinSpanName brackets one write_stdin interaction.
	UnifiedExecWriteStdinSpanName = "unified_exec.write_stdin"
	// UnifiedExecOpenSessionSpanName brackets creating one unified-exec session.
	UnifiedExecOpenSessionSpanName = "unified_exec.open_session"
	// UnifiedExecCollectOutputSpanName brackets one output collection.
	UnifiedExecCollectOutputSpanName = "unified_exec.collect_output"
)

// UnifiedExec span field names (Rust's span fields).
const (
	UnifiedExecSpanConversationID     = "conversation.id"
	UnifiedExecSpanTurnID             = "turn_id"
	UnifiedExecSpanCallID             = "call_id"
	UnifiedExecSpanOriginalExecCallID = "original_exec_call_id"
	UnifiedExecSpanProcessID          = "unified_exec_process_id"
	UnifiedExecSpanMode               = "mode"
	UnifiedExecSpanOutcome            = "outcome"
	UnifiedExecSpanInteraction        = "interaction"
	UnifiedExecSpanStopReason         = "stop_reason"
	UnifiedExecSpanExitSignaled       = "exit_signaled"
	UnifiedExecSpanOutputClosed       = "output_closed"
	// UnifiedExecSpanExecutorProcessKey is Rust's `process.id`: the executor's
	// own process id, which can differ from the public unified-exec id when a
	// sandbox retry reuses it.
	UnifiedExecSpanExecutorProcessKey = "process.id"
	// UnifiedExecProcessStartRequestedEvent is the trace-safe event Rust emits
	// before an exec-server process start (#45505).
	UnifiedExecProcessStartRequestedEvent = "codex.unified_exec.process_start_requested"
)

// Unified-exec span field values (Rust's literals).
const (
	UnifiedExecModeOneshot      = "oneshot"
	UnifiedExecModeResumable    = "resumable"
	UnifiedExecOutcomeExited    = "exited"
	UnifiedExecOutcomeYielded   = "yielded"
	UnifiedExecOutcomeCancelled = "cancelled"
	UnifiedExecOutcomeFailed    = "failed"
	UnifiedExecOutcomeTimedOut  = "timed_out"
	UnifiedExecOutcomeCompleted = "completed"
	// UnifiedExecInteractionPoll is reported when write_stdin sent no input.
	UnifiedExecInteractionPoll = "poll"
	// UnifiedExecInteractionWrite is reported when write_stdin sent input.
	UnifiedExecInteractionWrite = "write"
	// UnifiedExecStopReasonOutputClosed means the process exited and its output
	// streams closed.
	UnifiedExecStopReasonOutputClosed = "output_closed"
	// UnifiedExecStopReasonDeadline means the collection deadline expired while
	// the process was still running.
	UnifiedExecStopReasonDeadline = "deadline"
)

// UnifiedExecSpan is one open tracing span the unified-exec pipeline reports to.
// The app-server binds it to the session's tracer; a span from a pipeline with no
// sink records nothing.
type UnifiedExecSpan interface {
	SetAttribute(key string, value string)
	// AddEvent records a trace-safe event on the span, the way Rust's
	// `tracing::event!` with a trace-only target does.
	AddEvent(name string, attributes map[string]string)
	End()
}

// UnifiedExecSpanSink opens a unified-exec span with its initial attributes.
// Rust opens these spans through `#[tracing::instrument]`; the sink is the Go
// seam that keeps the tracer out of the tool layer.
type UnifiedExecSpanSink func(name string, attributes map[string]string) UnifiedExecSpan

// TracerUnifiedExecSpan is the span surface a tracing client provides
// (telemetry.Span): attribute writes, timestamped events, and End.
type TracerUnifiedExecSpan interface {
	SetAttribute(key string, value string)
	AddEvent(name string, attributes map[string]string, at time.Time)
	End()
}

// AdaptUnifiedExecSpan binds a tracer span to the pipeline's span surface,
// stamping an event with the time it is recorded.
func AdaptUnifiedExecSpan(span TracerUnifiedExecSpan) UnifiedExecSpan {
	if span == nil {
		return noopUnifiedExecSpan{}
	}
	return tracerUnifiedExecSpan{span: span}
}

type tracerUnifiedExecSpan struct {
	span TracerUnifiedExecSpan
}

func (s tracerUnifiedExecSpan) SetAttribute(key string, value string) {
	s.span.SetAttribute(key, value)
}

func (s tracerUnifiedExecSpan) AddEvent(name string, attributes map[string]string) {
	s.span.AddEvent(name, attributes, time.Now())
}

func (s tracerUnifiedExecSpan) End() {
	s.span.End()
}

// noopUnifiedExecSpan records nothing, the way a pipeline without a tracing
// subscriber reports nothing.
type noopUnifiedExecSpan struct{}

func (noopUnifiedExecSpan) SetAttribute(string, string)        {}
func (noopUnifiedExecSpan) AddEvent(string, map[string]string) {}
func (noopUnifiedExecSpan) End()                               {}

// UnifiedExecTraceID mirrors Rust's `trace_id`: an id is reported only when it
// carries an identity and fits the byte budget.
func UnifiedExecTraceID(id string) (string, bool) {
	trimmed := strings.TrimSpace(id)
	if trimmed == "" || len(trimmed) > MaxUnifiedExecTraceIDBytes {
		return "", false
	}
	return trimmed, true
}

// startUnifiedExecSpan opens one unified-exec span, reporting a no-op span when
// the pipeline has no sink so callers can always defer End.
func startUnifiedExecSpan(sink UnifiedExecSpanSink, name string, attributes map[string]string) UnifiedExecSpan {
	if sink == nil {
		return noopUnifiedExecSpan{}
	}
	span := sink(name, attributes)
	if span == nil {
		return noopUnifiedExecSpan{}
	}
	return span
}

// unifiedExecSpanAttributes builds the correlation attributes every unified-exec
// span carries: the conversation and turn, and the call that opened it. Rust
// omits ids that are empty or longer than 256 bytes.
func unifiedExecSpanAttributes(threadID string, turnID string, callID string) map[string]string {
	attributes := map[string]string{}
	if conversationID, ok := UnifiedExecTraceID(threadID); ok {
		attributes[UnifiedExecSpanConversationID] = conversationID
	}
	if turn, ok := UnifiedExecTraceID(turnID); ok {
		attributes[UnifiedExecSpanTurnID] = turn
	}
	if call, ok := UnifiedExecTraceID(callID); ok {
		attributes[UnifiedExecSpanCallID] = call
	}
	return attributes
}

// SetSpanSink installs the sink the unified-exec pipeline opens its lifecycle
// spans through. A nil sink leaves the pipeline untraced.
func (m *UnifiedExecManager) SetSpanSink(sink UnifiedExecSpanSink) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.spanSink = sink
}

// SpanSink reports the installed span sink (nil when the pipeline is untraced),
// so a caller that runs a command outside the manager can still open the
// exec_command span under the same trace.
func (m *UnifiedExecManager) SpanSink() UnifiedExecSpanSink {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.spanSink
}

// spanSinkSnapshot reads the installed span sink under the manager lock.
func (m *UnifiedExecManager) spanSinkSnapshot() UnifiedExecSpanSink {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.spanSink
}

// unifiedExecCallID reports an invocation's call id, tolerating a nil invocation.
func unifiedExecCallID(invocation *Invocation) string {
	if invocation == nil {
		return ""
	}
	return invocation.CallID
}

// unifiedExecOperationCancelled reports whether a failed unified-exec step failed
// because its caller went away. Rust asks the turn's cancellation token; Go's
// steps observe the same signal through their context.
func unifiedExecOperationCancelled(err error, ctx context.Context) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	return ctx != nil && ctx.Err() != nil
}

// unifiedExecCommandOutcome mirrors Rust's per-mode outcome for one exec_command
// call: the resumable path reports whether the call yielded a live process, and
// the completion-only path distinguishes a timed-out command.
func unifiedExecCommandOutcome(mode string, result *ShellResult, err error, ctx context.Context) string {
	if err != nil {
		if unifiedExecOperationCancelled(err, ctx) {
			return UnifiedExecOutcomeCancelled
		}
		return UnifiedExecOutcomeFailed
	}
	if result == nil {
		return UnifiedExecOutcomeFailed
	}
	if mode == UnifiedExecModeResumable && result.ProcessID != nil {
		return UnifiedExecOutcomeYielded
	}
	if result.TimedOut {
		return UnifiedExecOutcomeTimedOut
	}
	return UnifiedExecOutcomeExited
}

// unifiedExecStdinOutcome mirrors Rust's write_stdin outcome: it shares the
// resumable exec_command rule (a live process means the interaction yielded).
func unifiedExecStdinOutcome(result *ShellResult, err error, ctx context.Context) string {
	return unifiedExecCommandOutcome(UnifiedExecModeResumable, result, err, ctx)
}

// unifiedExecSessionOutcome mirrors Rust's open_session outcome.
func unifiedExecSessionOutcome(err error, ctx context.Context) string {
	if err == nil {
		return UnifiedExecOutcomeCompleted
	}
	if unifiedExecOperationCancelled(err, ctx) {
		return UnifiedExecOutcomeCancelled
	}
	return UnifiedExecOutcomeFailed
}

// unifiedExecSessionTrace is one session-creation span: Rust's
// `unified_exec.open_session`, which wraps the sandboxed spawn and carries the
// executor process-start event.
type unifiedExecSessionTrace struct {
	span UnifiedExecSpan
}

// startUnifiedExecSessionTrace opens the session-creation span.
func startUnifiedExecSessionTrace(m *UnifiedExecManager, threadID string, turnID string, callID string) unifiedExecSessionTrace {
	return unifiedExecSessionTrace{span: startUnifiedExecSpan(m.spanSinkSnapshot(), UnifiedExecOpenSessionSpanName,
		unifiedExecSpanAttributes(threadID, turnID, callID))}
}

// processStartRequested records Rust's trace-safe process-start event, which a
// sandbox retry reusing the public id for a new executor process makes worth
// reporting.
func (t unifiedExecSessionTrace) processStartRequested(processID int, executorProcessID string) {
	t.span.AddEvent(UnifiedExecProcessStartRequestedEvent, map[string]string{
		UnifiedExecSpanProcessID:          strconv.Itoa(processID),
		UnifiedExecSpanExecutorProcessKey: executorProcessID,
	})
}

// finish closes the span with the outcome of the session creation.
func (t unifiedExecSessionTrace) finish(err error, ctx context.Context) {
	t.span.SetAttribute(UnifiedExecSpanOutcome, unifiedExecSessionOutcome(err, ctx))
	t.span.End()
}
