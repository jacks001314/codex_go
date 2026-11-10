package model

import (
	"context"
	"net/http"
	"testing"

	"codex_go/protocol"
)

const (
	fallbackTraceparent = "00-0000000000000000000000000000000a-000000000000000b-01"
	activeTraceparent   = "00-0000000000000000000000000000000c-000000000000000d-01"
)

// Rust #60506: an inbound request carrier is forwarded even when no tracing
// layer produced an active span.
func TestInjectTraceHeadersFallsBackToRequestTraceLikeRust(t *testing.T) {
	headers := http.Header{}
	ctx := protocol.WithTraceContext(context.Background(), &protocol.W3CTraceContext{
		Traceparent: fallbackTraceparent,
		Tracestate:  "vendor=value",
	})
	if !InjectTraceHeadersFromContext(ctx, headers) {
		t.Fatal("request trace context was not forwarded")
	}
	if got := headers.Get("traceparent"); got != fallbackTraceparent {
		t.Fatalf("traceparent = %q, want %q", got, fallbackTraceparent)
	}
	if got := headers.Get("tracestate"); got != "vendor=value" {
		t.Fatalf("tracestate = %q, want vendor=value", got)
	}
}

// The active span wins over the request fallback, mirroring Rust's
// current_span_w3c_trace_context().or_else(request fallback).
func TestInjectTraceHeadersPrefersActiveSpanLikeRust(t *testing.T) {
	headers := http.Header{}
	ctx := protocol.WithTraceContext(context.Background(), &protocol.W3CTraceContext{
		Traceparent: fallbackTraceparent,
		Tracestate:  "fallback=1",
	})
	ctx = WithTelemetryTraceCarrier(ctx, &recordingTelemetrySpan{traceparent: activeTraceparent})
	if !InjectTraceHeadersFromContext(ctx, headers) {
		t.Fatal("active span trace context was not forwarded")
	}
	if got := headers.Get("traceparent"); got != activeTraceparent {
		t.Fatalf("traceparent = %q, want the active span value", got)
	}
	if got := headers.Get("tracestate"); got != "" {
		t.Fatalf("tracestate = %q, want the fallback value to be replaced", got)
	}
}

func TestInjectTraceHeadersWithoutAnyCarrierLikeRust(t *testing.T) {
	headers := http.Header{}
	if InjectTraceHeadersFromContext(context.Background(), headers) {
		t.Fatal("no carrier must not report injected headers")
	}
	if headers.Get("traceparent") != "" {
		t.Fatal("no carrier must not set traceparent")
	}
	empty := protocol.WithTraceContext(context.Background(), &protocol.W3CTraceContext{Tracestate: "only=state"})
	if InjectTraceHeadersFromContext(empty, headers) {
		t.Fatal("a tracestate without traceparent must not inject headers")
	}
}
