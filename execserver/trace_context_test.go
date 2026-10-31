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

func TestTraceContextFromW3CLikeRust(t *testing.T) {
	trace, ok := TraceContextFromW3C("00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01")
	if !ok || trace.TraceID != "4bf92f3577b34da6a3ce929d0e0e4736" || trace.SpanID != "00f067aa0ba902b7" {
		t.Fatalf("W3C trace = %#v, ok = %v", trace, ok)
	}
	for _, traceparent := range []string{
		"",
		"not-a-traceparent",
		"00-00000000000000000000000000000000-00f067aa0ba902b7-01",
		"00-4bf92f3577b34da6a3ce929d0e0e4736-0000000000000000-01",
		"00-4bf92f3577b34da6a3ce929d0e0e473z-00f067aa0ba902b7-01",
	} {
		if trace, ok := TraceContextFromW3C(traceparent); ok || !trace.IsZero() {
			t.Fatalf("traceparent %q parsed as %#v", traceparent, trace)
		}
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

func TestClientRequestEnvelopeCarriesTraceLikeRust(t *testing.T) {
	withTrace := clientRequestEnvelope(7, MethodEnvironmentInfo, map[string]any{"cwd": "/repo"}, TraceContext{TraceID: "trace-1", SpanID: "span-1"})
	trace, ok := withTrace["traceContext"].(TraceContext)
	if !ok || trace.TraceID != "trace-1" || trace.SpanID != "span-1" {
		t.Fatalf("envelope traceContext = %#v", withTrace["traceContext"])
	}
	if withTrace["method"] != MethodEnvironmentInfo || withTrace["id"] != int64(7) {
		t.Fatalf("envelope = %#v", withTrace)
	}
	withoutTrace := clientRequestEnvelope(8, MethodEnvironmentInfo, nil, TraceContext{})
	if _, present := withoutTrace["traceContext"]; present {
		t.Fatalf("zero trace leaked into envelope: %#v", withoutTrace)
	}
}

func TestResponseEncodesTraceContext(t *testing.T) {
	trace := TraceContext{TraceID: "trace-1", SpanID: "span-1"}
	data, err := json.Marshal(response{ID: RequestID{}, Result: "ok", TraceContext: &trace})
	if err != nil {
		t.Fatalf("Marshal(response) error = %v", err)
	}
	var decoded struct {
		TraceContext *TraceContext `json:"traceContext"`
	}
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal(response) error = %v", err)
	}
	if decoded.TraceContext == nil || decoded.TraceContext.TraceID != "trace-1" {
		t.Fatalf("decoded trace = %#v", decoded.TraceContext)
	}
}
