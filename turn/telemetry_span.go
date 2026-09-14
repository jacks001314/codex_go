package turn

import (
	"context"
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
	return request.Tracer.StartSpan(ctx, nil, SamplingRequestSpanName, attributes)
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
