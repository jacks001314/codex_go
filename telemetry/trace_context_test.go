package telemetry

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// Rust's trace_context tests: a valid carrier yields the trace/span pair, an
// invalid or missing traceparent yields nothing.
func TestParseTraceContextLikeRust(t *testing.T) {
	context, ok := ParseTraceContext("00-00000000000000000000000000000001-0000000000000002-01", "")
	if !ok {
		t.Fatal("a valid traceparent should parse")
	}
	if context.TraceID != "00000000000000000000000000000001" ||
		context.SpanID != "0000000000000002" || !context.Sampled {
		t.Fatalf("context = %#v", context)
	}

	for _, traceparent := range []string{
		"not-a-traceparent",
		"",
		"00-00000000000000000000000000000000-0000000000000002-01",
		"00-00000000000000000000000000000001-0000000000000000-01",
		"00-00000000000000000000000000000001-0000000000000002",
		"00-ABCDEFABCDEFABCDEFABCDEFABCDEFAB-0000000000000002-01",
		"ff-00000000000000000000000000000001-0000000000000002-01",
	} {
		if _, ok := ParseTraceContext(traceparent, ""); ok {
			t.Fatalf("traceparent %q should be rejected", traceparent)
		}
	}
}

// A malformed tracestate is dropped while the valid traceparent still parses
// (the propagator replaces an unparsable state with an empty one).
func TestParseTraceContextDropsMalformedTracestate(t *testing.T) {
	context, ok := ParseTraceContext(
		"00-00000000000000000000000000000001-0000000000000002-01",
		"not-a-member",
	)
	if !ok || context.TraceState != "" {
		t.Fatalf("context = %#v ok = %v", context, ok)
	}
	context, ok = ParseTraceContext(
		"00-00000000000000000000000000000001-0000000000000002-01",
		"example=alpha:zero;keep:yes,other=value",
	)
	if !ok || context.TraceState != "example=alpha:zero;keep:yes,other=value" {
		t.Fatalf("context = %#v ok = %v", context, ok)
	}
}

// The loopback expectation from Rust's otlp_http_loopback trace test: the
// configured fields are upserted inside their member and unrelated members stay
// untouched.
func TestMergeTracestateEntriesLikeRust(t *testing.T) {
	configured := map[string]map[string]string{
		"example": {"alpha": "one", "beta": "two"},
	}
	merged := MergeTracestateEntries("example=alpha:zero;keep:yes,other=value", configured)
	if merged != "example=alpha:one;keep:yes;beta:two,other=value" {
		t.Fatalf("merged = %q", merged)
	}
	if got := MergeTracestateEntries("", configured); got != "example=alpha:one;beta:two" {
		t.Fatalf("merged from empty = %q", got)
	}
	if got := MergeTracestateEntries("other=value", nil); got != "other=value" {
		t.Fatalf("merged without configured = %q", got)
	}
}

// Rust's loopback test rejects a configured tracestate value that cannot travel
// in a header.
func TestValidateTracestateEntriesRejectsHeaderUnsafeValue(t *testing.T) {
	err := ValidateTracestateEntries(map[string]map[string]string{
		"example": {"alpha": "one\ntwo"},
	})
	if err == nil || !strings.Contains(err.Error(), "configured tracestate value") {
		t.Fatalf("error = %v", err)
	}
	if err := ValidateTracestateEntries(map[string]map[string]string{
		"example": {"alpha": "one", "beta": "two"},
	}); err != nil {
		t.Fatalf("valid entries error = %v", err)
	}
}

// Rust's try_new validates the configured tracestate before any pipeline is
// built, so an unsafe member fails the provider construction.
func TestNewOtelProviderRejectsHeaderUnsafeTracestate(t *testing.T) {
	provider, err := NewOtelProvider(OtelSettings{
		ServiceName:   "codex-cli",
		TraceExporter: OtelExporter{Kind: OtelExporterOtlpHTTP, Endpoint: "http://127.0.0.1:1/v1/traces"},
		Tracestate:    map[string]map[string]string{"example": {"alpha": "one\ntwo"}},
	})
	if err == nil || provider != nil {
		t.Fatalf("provider = %#v error = %v", provider, err)
	}
	if !strings.Contains(err.Error(), "configured tracestate value") {
		t.Fatalf("error = %v", err)
	}
}

// An enabled provider installs the configured tracestate; a disabled one clears
// the process-global entries.
func TestOtelProviderInstallsAndClearsTracestate(t *testing.T) {
	provider, err := NewOtelProvider(OtelSettings{
		ServiceName:   "codex-cli",
		TraceExporter: OtelExporter{Kind: OtelExporterOtlpHTTP, Endpoint: "http://127.0.0.1:1/v1/traces"},
		Tracestate:    map[string]map[string]string{"example": {"alpha": "one"}},
	})
	if err != nil || provider == nil {
		t.Fatalf("provider = %#v error = %v", provider, err)
	}
	if err := provider.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}
	if got := TracestateEntries(); got["example"]["alpha"] != "one" {
		t.Fatalf("installed entries = %#v", got)
	}

	disabled, err := NewOtelProvider(OtelSettings{
		TraceExporter:   OtelExporter{Kind: OtelExporterNone},
		Exporter:        OtelExporter{Kind: OtelExporterNone},
		MetricsExporter: OtelExporter{Kind: OtelExporterNone},
	})
	if err != nil || disabled != nil {
		t.Fatalf("disabled provider = %#v error = %v", disabled, err)
	}
	if got := TracestateEntries(); len(got) != 0 {
		t.Fatalf("cleared entries = %#v", got)
	}
}

// TRACEPARENT / TRACESTATE seed the process trace context once, like Rust's
// TRACEPARENT_CONTEXT.
func TestTraceContextFromEnvLikeRust(t *testing.T) {
	t.Setenv(TraceparentEnvVar, "00-00000000000000000000000000000001-0000000000000002-01")
	t.Setenv(TracestateEnvVar, "example=alpha:zero")
	resetTraceContextEnvCache()
	defer resetTraceContextEnvCache()

	context, ok := TraceContextFromEnv()
	if !ok || context.TraceID != "00000000000000000000000000000001" ||
		context.SpanID != "0000000000000002" || context.TraceState != "example=alpha:zero" {
		t.Fatalf("env context = %#v ok = %v", context, ok)
	}
}

func TestTraceContextFromEnvIgnoresInvalidTraceparent(t *testing.T) {
	t.Setenv(TraceparentEnvVar, "not-a-traceparent")
	t.Setenv(TracestateEnvVar, "")
	resetTraceContextEnvCache()
	defer resetTraceContextEnvCache()

	if context, ok := TraceContextFromEnv(); ok {
		t.Fatalf("env context = %#v", context)
	}
}

// A span that continues a remote trace adopts its trace id, parent span id, and
// tracestate, and inherits the remote sampling decision.
func TestSpanParentContextAndPropagation(t *testing.T) {
	SetTracestateEntries(map[string]map[string]string{"example": {"alpha": "one"}})
	defer SetTracestateEntries(nil)

	client := NewTracesClient(TracesClientOptions{
		ServiceName:    "codex-app-server",
		Endpoint:       "http://127.0.0.1:1/v1/traces",
		ExportInterval: -1,
	})
	tracer := client.Tracer()
	parent, ok := ParseTraceContext(
		"00-00000000000000000000000000000001-0000000000000002-01",
		"example=alpha:zero;keep:yes,other=value",
	)
	if !ok {
		t.Fatal("parent context should parse")
	}
	span := tracer.StartSpan("app_server.request", nil)
	if !span.SetParentContext(parent) {
		t.Fatal("SetParentContext() = false")
	}
	if span.TraceID != parent.TraceID || span.ParentSpanID != parent.SpanID {
		t.Fatalf("span = %#v", span)
	}
	headers := http.Header{}
	if !span.InjectTraceHeaders(headers) {
		t.Fatal("InjectTraceHeaders() = false")
	}
	if got := headers.Get(TraceparentHeader); got != "00-00000000000000000000000000000001-"+span.SpanID+"-01" {
		t.Fatalf("traceparent = %q", got)
	}
	if got := headers.Get(TracestateHeader); got != "example=alpha:one;keep:yes,other=value" {
		t.Fatalf("tracestate = %q", got)
	}

	// A non-sampled remote parent is not recorded: the span is dropped instead
	// of exported (Rust's parent-based sampler).
	unsampled, ok := ParseTraceContext(
		"00-00000000000000000000000000000001-0000000000000002-00", "")
	if !ok {
		t.Fatal("unsampled parent context should parse")
	}
	dropped := tracer.StartSpan("dropped", nil)
	dropped.SetParentContext(unsampled)
	dropped.End()
	if client.Dropped() != 0 {
		t.Fatalf("dropped counter = %d", client.Dropped())
	}
	if err := client.Flush(context.Background()); err != nil {
		t.Fatalf("Flush() error = %v", err)
	}
}

// The exported span carries the parent's tracestate and shares the inbound
// trace id, like Rust's span context.
func TestExportedSpanCarriesInboundTraceContext(t *testing.T) {
	bodies := make(chan map[string]any, 1)
	server := newTraceBatchServer(t, bodies)
	defer server.Close()

	client := NewTracesClient(TracesClientOptions{
		ServiceName:    "codex-app-server",
		Endpoint:       server.URL + "/v1/traces",
		ExportInterval: -1,
	})
	tracer := client.Tracer()
	parent, ok := ParseTraceContext(
		"00-00000000000000000000000000000001-0000000000000002-01", "other=value")
	if !ok {
		t.Fatal("parent context should parse")
	}
	span := tracer.StartSpan("app_server.request", nil)
	span.SetParentContext(parent)
	span.End()
	if err := client.Flush(context.Background()); err != nil {
		t.Fatalf("Flush() error = %v", err)
	}

	select {
	case body := <-bodies:
		spans := body["resourceSpans"].([]any)[0].(map[string]any)["scopeSpans"].([]any)[0].(map[string]any)["spans"].([]any)
		exported := spans[0].(map[string]any)
		if exported["traceId"] != parent.TraceID || exported["parentSpanId"] != parent.SpanID {
			t.Fatalf("span = %#v", exported)
		}
		if exported["traceState"] != "other=value" {
			t.Fatalf("traceState = %#v", exported["traceState"])
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the trace endpoint did not receive the span")
	}
}

func TestSpanTraceContextWithoutValidIds(t *testing.T) {
	span := &Span{TraceID: "0", SpanID: "0"}
	if _, ok := span.TraceContext(); ok {
		t.Fatal("a span without valid ids should report no context")
	}
	var nilSpan *Span
	if nilSpan.InjectTraceHeaders(http.Header{}) {
		t.Fatal("a nil span should not inject headers")
	}
}

// newTraceBatchServer captures the first OTLP/HTTP trace export it receives.
func newTraceBatchServer(t *testing.T, bodies chan<- map[string]any) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		payload := map[string]any{}
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Errorf("decode trace export error = %v", err)
		}
		select {
		case bodies <- payload:
		default:
		}
		writer.WriteHeader(http.StatusOK)
	}))
}
