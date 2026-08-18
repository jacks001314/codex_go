package execserver

import (
	"context"
	"log/slog"
	"strings"

	"github.com/google/uuid"
)

// TraceContext carries a request trace identifier from receipt through
// completion (Rust exec-server trace_context.rs, #39098). codex_go has no OTLP
// span tree, so TraceID/SpanID propagate through the existing JSON-RPC
// request/response chain and are exposed on server-side request handling.
type TraceContext struct {
	TraceID string `json:"traceId,omitempty"`
	SpanID  string `json:"spanId,omitempty"`
}

type traceContextKey struct{}

// NewTraceContext generates a fresh trace/span pair for an inbound request.
func NewTraceContext() TraceContext {
	return TraceContext{TraceID: uuid.NewString(), SpanID: uuid.NewString()}
}

// IsZero reports whether the context carries no identifiers.
func (t TraceContext) IsZero() bool {
	return strings.TrimSpace(t.TraceID) == ""
}

// String renders the W3C-style traceparent tail "traceId;spanId".
func (t TraceContext) String() string {
	return strings.TrimSpace(t.TraceID) + ";" + strings.TrimSpace(t.SpanID)
}

// ParseTraceContext parses "traceId;spanId" (Rust trace-parent propagation).
func ParseTraceContext(raw string) TraceContext {
	parts := strings.Split(strings.TrimSpace(raw), ";")
	if len(parts) != 2 {
		return TraceContext{}
	}
	return TraceContext{TraceID: strings.TrimSpace(parts[0]), SpanID: strings.TrimSpace(parts[1])}
}

// ChildSpan returns a child span identifier within the same trace.
func (t TraceContext) ChildSpan() TraceContext {
	return TraceContext{TraceID: strings.TrimSpace(t.TraceID), SpanID: uuid.NewString()}
}

func withTraceContext(ctx context.Context, trace TraceContext) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, traceContextKey{}, trace)
}

func traceContextFromContext(ctx context.Context) TraceContext {
	if ctx == nil {
		return TraceContext{}
	}
	trace, _ := ctx.Value(traceContextKey{}).(TraceContext)
	return trace
}

// logEnvironmentTrace exposes the active request trace on environment
// resolution RPCs (Rust #39078: environment resolution retains tracing
// context); the same trace is carried back on the RPC response.
func logEnvironmentTrace(ctx context.Context, method string) {
	trace := traceContextFromContext(ctx)
	if trace.IsZero() {
		return
	}
	slog.Debug("exec-server environment request", "method", method, "trace_id", trace.TraceID, "span_id", trace.SpanID)
}
