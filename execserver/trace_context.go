package execserver

import (
	"context"
	"log/slog"
	"strings"

	"github.com/google/uuid"
)

// TraceContext carries a request trace identifier from receipt through
// completion (Rust exec-server trace_context.rs, #39098). The identifiers travel
// through the exec-server's own JSON-RPC request/response chain; the W3C carrier
// that crosses the code-mode gRPC boundary comes from the active span instead
// (protocol.W3CTraceContext).
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

// TraceContextFromW3C extracts the trace and parent span identifiers from a
// W3C traceparent carrier. Malformed or all-zero identifiers are rejected so a
// launch never inherits an unrelated active span.
func TraceContextFromW3C(traceparent string) (TraceContext, bool) {
	parts := strings.Split(strings.TrimSpace(traceparent), "-")
	if len(parts) != 4 || len(parts[0]) != 2 || !isHex(parts[0]) || !isHex(parts[1]) || !isHex(parts[2]) || len(parts[3]) != 2 || !isHex(parts[3]) {
		return TraceContext{}, false
	}
	if len(parts[1]) != 32 || len(parts[2]) != 16 {
		return TraceContext{}, false
	}
	if strings.Trim(parts[1], "0") == "" || strings.Trim(parts[2], "0") == "" {
		return TraceContext{}, false
	}
	return TraceContext{TraceID: parts[1], SpanID: parts[2]}, true
}

func isHex(value string) bool {
	for index := 0; index < len(value); index++ {
		character := value[index]
		if character >= '0' && character <= '9' || character >= 'a' && character <= 'f' || character >= 'A' && character <= 'F' {
			continue
		}
		return false
	}
	return value != ""
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

// TraceContextFromContext returns the trace context carried in ctx, if any
// (Rust exec-server trace_context.rs, #39098): the exported accessor for the
// exec-server's own request tracing.
func TraceContextFromContext(ctx context.Context) TraceContext {
	return traceContextFromContext(ctx)
}

// WithTraceContext carries a trace context in ctx (Rust exec-server
// trace_context.rs, #39098): the exported setter for the exec-server's own
// request tracing.
func WithTraceContext(ctx context.Context, trace TraceContext) context.Context {
	return withTraceContext(ctx, trace)
}
