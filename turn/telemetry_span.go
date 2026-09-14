package turn

import (
	"context"
	"strings"

	"codex_go/model"
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
