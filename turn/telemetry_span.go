package turn

import (
	"context"
	"encoding/json"
	"sort"
	"strings"

	"codex_go/model"
	"codex_go/protocol"
)

// Rust parity: the per-turn spans codex-core's turn loop opens (core/src/session/turn.rs
// instruments `run_sampling_request` with the turn id, the model slug, and the
// working directory; the tracing macros parent them to the ambient span).

// SpanTracer starts the turn's spans. The model package's TelemetrySpan is
// reused so one implementation satisfies both the model client's and the turn
// loop's sink.
type SpanTracer interface {
	StartSpan(ctx context.Context, parent model.TelemetrySpan, name string, attributes map[string]string) (context.Context, model.TelemetrySpan)
}

// TraceEventEmitter records a trace-safe event on the span the context carries
// (Rust's `tracing::event!(target: "codex_otel.trace_safe")`). The turn loop
// uses it for the events Rust emits from inside the sampling request
// (`codex.mailbox_preemption`, Rust #49262/#51249). A tracer that does not
// implement it drops the event, like a process without a tracing subscriber.
type TraceEventEmitter interface {
	EmitTraceSafeEvent(ctx context.Context, eventName string, fields map[string]string)
}

// SamplingRequestSpanName mirrors the span Rust's `run_sampling_request`
// instrumentation reports.
const SamplingRequestSpanName = "run_sampling_request"

// startSamplingRequestSpan opens the per-sampling-request span and returns the
// context carrying it, or the caller's context when the turn has no tracer.
func startSamplingRequestSpan(ctx context.Context, request *AgentLoopRequest, modelID string) (context.Context, model.TelemetrySpan) {
	if request == nil || request.Tracer == nil {
		return ctx, nil
	}
	attributes := map[string]string{
		"turn_id": strings.TrimSpace(request.TurnID),
		"model":   strings.TrimSpace(modelID),
	}
	if strings.TrimSpace(request.CWD) != "" {
		attributes["cwd"] = strings.TrimSpace(request.CWD)
	}
	// Rust #46501: the sampling span carries the scalar usage diagnostics as a
	// serialized document so the feedback collector can expand them.
	if encoded := usageTagsDocument(request.UsageTags); encoded != "" {
		attributes["tags_json"] = encoded
	}
	return request.Tracer.StartSpan(ctx, nil, SamplingRequestSpanName, attributes)
}

// Rust #49262 (a7660cd154): turn-phase spans carry `codex.turn.phase` plus the
// conversation and turn ids, so a trace can tell sampling time from time spent
// waiting for tools and correlate a phase with the turn that owns it.
const (
	// TurnPhaseAttribute is the span attribute naming the turn phase.
	TurnPhaseAttribute = "codex.turn.phase"
	// ConversationIDAttribute is the span attribute carrying the thread id.
	ConversationIDAttribute = "conversation.id"
	// TurnIDAttribute is the span attribute carrying the turn id.
	TurnIDAttribute = "turn.id"
)

// The phases Rust brackets with their own spans: sampling ends before the
// step's tools drain, and compaction runs in its own span.
const (
	TurnPhaseSampling     = "sampling"
	TurnPhaseToolBlocking = "tool_blocking"
	TurnPhaseCompaction   = "compaction"
)

// The phase span names Rust's `trace_span!` macros report.
const (
	SamplingPhaseSpanName     = "codex.sampling"
	ToolBlockingPhaseSpanName = "codex.tool_blocking"
	CompactionPhaseSpanName   = "codex.compaction"
)

// startTurnPhaseSpan opens one of the turn's phase spans
// (`codex.sampling` / `codex.tool_blocking` / `codex.compaction`) nested under
// the given parent span. Rust's `trace_span!` creates the span without entering
// it, so the caller keeps the ambient span it already had: the phase marker
// brackets the phase but does not become the parent of the work inside it.
func startTurnPhaseSpan(ctx context.Context, request *AgentLoopRequest, parent model.TelemetrySpan, name string, phase string, threadID string, turnID string) model.TelemetrySpan {
	if request == nil || request.Tracer == nil {
		return nil
	}
	attributes := map[string]string{
		TurnPhaseAttribute:      phase,
		ConversationIDAttribute: strings.TrimSpace(threadID),
		TurnIDAttribute:         strings.TrimSpace(turnID),
	}
	_, span := request.Tracer.StartSpan(ctx, parent, name, attributes)
	return span
}

// usageTagsDocument serializes the usage-tag document with sorted keys, the way
// Rust's span field renders the JSON map.
func usageTagsDocument(tags map[string]string) string {
	if len(tags) == 0 {
		return ""
	}
	keys := make([]string, 0, len(tags))
	for key := range tags {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var builder strings.Builder
	builder.WriteByte('{')
	for index, key := range keys {
		if index > 0 {
			builder.WriteByte(',')
		}
		encodedKey, err := json.Marshal(key)
		if err != nil {
			return ""
		}
		encodedValue, err := json.Marshal(tags[key])
		if err != nil {
			return ""
		}
		builder.Write(encodedKey)
		builder.WriteByte(':')
		builder.Write(encodedValue)
	}
	builder.WriteByte('}')
	return builder.String()
}

// withSpanTraceContext stores the span's W3C carrier in the context, so anything
// that crosses a process boundary (the code-mode gRPC session) propagates the
// span context Rust reads from the current span.
func withSpanTraceContext(ctx context.Context, span model.TelemetrySpan) context.Context {
	if span == nil {
		return ctx
	}
	traceparent, tracestate, ok := span.TraceContext()
	if !ok {
		return ctx
	}
	return protocol.WithTraceContext(ctx, &protocol.W3CTraceContext{Traceparent: traceparent, Tracestate: tracestate})
}
