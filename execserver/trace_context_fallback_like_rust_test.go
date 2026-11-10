package execserver

import (
	"context"
	"testing"

	"codex_go/protocol"
)

// Rust #60506: exec-server RPCs fall back to the request's W3C carrier when no
// active span trace is installed, while an explicit exec-server trace still wins.
func TestTraceContextFallsBackToW3CRequestCarrierLikeRust(t *testing.T) {
	const traceparent = "00-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01"
	carrier := &protocol.W3CTraceContext{Traceparent: traceparent}

	trace := traceContextFromContext(protocol.WithTraceContext(context.Background(), carrier))
	if trace.TraceID != "0af7651916cd43dd8448eb211c80319c" || trace.SpanID != "b7ad6b7169203331" {
		t.Fatalf("fallback trace = %#v", trace)
	}

	explicit := withTraceContext(context.Background(), TraceContext{TraceID: "explicit-trace", SpanID: "explicit-span"})
	if got := traceContextFromContext(protocol.WithTraceContext(explicit, carrier)); got.TraceID != "explicit-trace" || got.SpanID != "explicit-span" {
		t.Fatalf("explicit trace lost: %#v", got)
	}

	if got := traceContextFromContext(protocol.WithTraceContext(context.Background(), &protocol.W3CTraceContext{Traceparent: "not-a-traceparent"})); !got.IsZero() {
		t.Fatalf("invalid carrier produced %#v", got)
	}
}
