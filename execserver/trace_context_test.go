package execserver

import (
	"context"
	"encoding/json"
	"testing"
)

func TestTraceContextRoundTripAndPropagation(t *testing.T) {
	trace := NewTraceContext()
	if trace.IsZero() {
		t.Fatal("NewTraceContext() produced a zero trace")
	}
	if trace.TraceID == trace.SpanID {
		t.Fatal("trace and span ids should differ")
	}
	parsed := ParseTraceContext(trace.String())
	if parsed != trace {
		t.Fatalf("parsed = %+v, want %+v", parsed, trace)
	}
	if !ParseTraceContext("").IsZero() || !ParseTraceContext("only-one-part").IsZero() {
		t.Fatal("malformed trace strings should parse to zero")
	}
	child := trace.ChildSpan()
	if child.TraceID != trace.TraceID || child.SpanID == trace.SpanID {
		t.Fatalf("child = %+v (parent %+v)", child, trace)
	}

	ctx := withTraceContext(context.Background(), trace)
	if got := traceContextFromContext(ctx); got != trace {
		t.Fatalf("ctx trace = %+v, want %+v", got, trace)
	}
	if got := traceContextFromContext(nil); !got.IsZero() {
		t.Fatalf("nil ctx trace = %+v, want zero", got)
	}
}

func TestRequestDecodesTraceContext(t *testing.T) {
	line := []byte(`{"id":1,"method":"environment/info","traceContext":{"traceId":"trace-1","spanId":"span-1"}}`)
	var req request
	if err := json.Unmarshal(line, &req); err != nil {
		t.Fatalf("Unmarshal(request) error = %v", err)
	}
	if req.TraceContext == nil || req.TraceContext.TraceID != "trace-1" || req.TraceContext.SpanID != "span-1" {
		t.Fatalf("request trace context = %#v", req.TraceContext)
	}
	// Requests without a trace context decode cleanly.
	var bare request
	if err := json.Unmarshal([]byte(`{"id":2,"method":"process/start"}`), &bare); err != nil {
		t.Fatalf("Unmarshal(bare) error = %v", err)
	}
	if bare.TraceContext != nil {
		t.Fatalf("bare request trace context = %#v, want nil", bare.TraceContext)
	}
}
