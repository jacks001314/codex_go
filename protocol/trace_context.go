package protocol

import "context"

// Rust parity: codex_protocol::protocol::W3cTraceContext.

// W3CTraceContext is the W3C trace-context carrier a client may attach to a
// request; it follows the request into the turn and from there into the model
// request, so one trace spans the client, the app server, and the model call.
type W3CTraceContext struct {
	Traceparent string `json:"traceparent,omitempty"`
	Tracestate  string `json:"tracestate,omitempty"`
}

// traceContextKey carries a W3C trace context through a context.
type traceContextKey struct{}

// WithTraceContext returns a context carrying the trace context, so callers that
// cross a process boundary (for example the code-mode gRPC session) can propagate
// the span context Rust reads from the current span.
func WithTraceContext(ctx context.Context, trace *W3CTraceContext) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if trace == nil || (trace.Traceparent == "" && trace.Tracestate == "") {
		return ctx
	}
	return context.WithValue(ctx, traceContextKey{}, trace)
}

// TraceContextFromContext returns the trace context carried by ctx, if any.
func TraceContextFromContext(ctx context.Context) (*W3CTraceContext, bool) {
	if ctx == nil {
		return nil, false
	}
	trace, ok := ctx.Value(traceContextKey{}).(*W3CTraceContext)
	if !ok || trace == nil {
		return nil, false
	}
	return trace, true
}
