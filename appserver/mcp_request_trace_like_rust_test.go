package appserver

import (
	"testing"

	"codex_go/protocol"
)

// Rust #60506 carries a validated inbound W3C carrier into MCP work so
// mcpServer/resource/read still forwards traceparent when tracing is disabled.
func TestRequestTraceContextCarriesValidatedW3CContextLikeRust(t *testing.T) {
	const traceparent = "00-00000000000000000000000000000001-0000000000000002-01"
	ctx := requestTraceContext(&Request{Trace: &W3CTraceContext{Traceparent: traceparent, Tracestate: "other=value"}})
	trace, ok := protocol.TraceContextFromContext(ctx)
	if !ok || trace.Traceparent != traceparent || trace.Tracestate != "other=value" {
		t.Fatalf("trace context = %#v (ok=%v)", trace, ok)
	}
	if _, ok := protocol.TraceContextFromContext(requestTraceContext(&Request{Trace: &W3CTraceContext{Traceparent: "not-a-traceparent"}})); ok {
		t.Fatal("an invalid inbound carrier must be dropped")
	}
	if _, ok := protocol.TraceContextFromContext(requestTraceContext(&Request{})); ok {
		t.Fatal("a request without a carrier must not invent one")
	}
	if _, ok := protocol.TraceContextFromContext(requestTraceContext(nil)); ok {
		t.Fatal("a nil request must not invent a carrier")
	}
}
