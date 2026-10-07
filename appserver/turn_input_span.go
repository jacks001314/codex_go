package appserver

import (
	"strings"

	"codex_go/telemetry"
	"codex_go/turn"
)

// Rust parity: codex-core instruments its session turn-input handler with
// `codex.turn_input`, a span carrying `conversation.id` and the accepted
// `turn.id` (core/src/session/turn_input.rs, #49262 a7660cd154). The app-server
// is where a turn's input is accepted and the turn is started or steered, so it
// opens the same span in the trace of the request that carried the input - the
// accepted turn may then run in the thread's own (different) trace.

// TurnInputSpanName is the span Rust's `#[tracing::instrument(name =
// "codex.turn_input")]` reports on the turn-input handler.
const TurnInputSpanName = "codex.turn_input"

// turnInputConversationIDAttribute / turnInputTurnIDAttribute are the span
// fields Rust declares for the accepted input.
const (
	turnInputConversationIDAttribute = "conversation.id"
	turnInputTurnIDAttribute         = "turn.id"
)

// startTurnInputSpan opens the `codex.turn_input` span for one accepted input
// request, nested under the request's span so the accepted input stays in the
// caller's trace. The turn id is not known yet, so it stays empty until
// endTurnInputSpan records it, exactly like Rust's span field. A router without
// an installed tracing provider has no span.
func (r *RuntimeRouter) startTurnInputSpan(parent *telemetry.Span, threadID string) *telemetry.Span {
	if r == nil {
		return nil
	}
	tracer := r.requestTracer()
	if tracer == nil {
		return nil
	}
	attributes := map[string]string{
		turnInputConversationIDAttribute: strings.TrimSpace(threadID),
		// Rust records the field as empty until the submission has a turn id.
		turnInputTurnIDAttribute: "",
	}
	if parent != nil {
		return tracer.StartSpanWithParent(parent, TurnInputSpanName, attributes)
	}
	return tracer.StartSpan(TurnInputSpanName, attributes)
}

// endTurnInputSpan records the accepted turn id (Rust's
// `Span::current().record("turn.id", turn_id)`) and closes the span.
func endTurnInputSpan(span *telemetry.Span, turnID string) {
	if span == nil {
		return
	}
	if turnID = strings.TrimSpace(turnID); turnID != "" {
		span.SetAttribute(turnInputTurnIDAttribute, turnID)
	}
	span.End()
}

// CompactionSpanName is the span Rust opens around a compaction task
// (`codex.compaction`, core/src/tasks/compact.rs, #49262) in addition to the
// automatic compaction span in the turn loop.
const CompactionSpanName = turn.CompactionPhaseSpanName

// startCompactionSpan opens the `codex.compaction` phase span for a manual
// compaction task (Go's analogue of Rust's `CompactTask::run`), carrying
// `codex.turn.phase = "compaction"` plus the conversation and turn ids. A
// router without an installed tracing provider has no span.
func (r *RuntimeRouter) startCompactionSpan(parent *telemetry.Span, threadID string, turnID string) *telemetry.Span {
	if r == nil {
		return nil
	}
	tracer := r.requestTracer()
	if tracer == nil {
		return nil
	}
	attributes := map[string]string{
		turn.TurnPhaseAttribute:      turn.TurnPhaseCompaction,
		turn.ConversationIDAttribute: strings.TrimSpace(threadID),
		turn.TurnIDAttribute:         strings.TrimSpace(turnID),
	}
	if parent != nil {
		return tracer.StartSpanWithParent(parent, CompactionSpanName, attributes)
	}
	return tracer.StartSpan(CompactionSpanName, attributes)
}
