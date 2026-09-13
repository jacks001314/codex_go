package telemetry

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	collectortracepb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	"google.golang.org/protobuf/proto"
)

// Rust's trace_export_filter excludes h2's explicit-root spans.
func TestIsTraceExportTargetLikeRust(t *testing.T) {
	for _, tc := range []struct {
		target string
		want   bool
	}{
		{target: "codex_core::client", want: true},
		{target: "codex_otel", want: true},
		{target: "", want: true},
		{target: "h2", want: false},
		{target: "h2::client", want: false},
	} {
		if got := IsTraceExportTarget(tc.target); got != tc.want {
			t.Fatalf("IsTraceExportTarget(%q) = %v, want %v", tc.target, got, tc.want)
		}
	}
}

// A span exported through the OTLP/HTTP JSON transport carries the resource,
// scope, ids, timings, attributes (including the configured span attributes),
// and status.
func TestTracesClientExportsSpansLikeRust(t *testing.T) {
	var bodies []map[string]any
	contentTypes := []string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		contentTypes = append(contentTypes, r.Header.Get("Content-Type"))
		payload, _ := io.ReadAll(r.Body)
		var body map[string]any
		if err := json.Unmarshal(payload, &body); err != nil {
			t.Errorf("span batch json error = %v payload=%s", err, payload)
		}
		bodies = append(bodies, body)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	now := time.Unix(1_700_000_000, 0).UTC()
	client := NewTracesClient(TracesClientOptions{
		ServiceName:    "codex-app-server",
		ServiceVersion: "1.2.3",
		Environment:    "dev",
		Endpoint:       server.URL + "/v1/traces",
		ExportInterval: -1,
		SpanAttributes: map[string]string{"deployment": "test"},
		Now:            func() time.Time { return now },
	})
	if !client.Enabled() {
		t.Fatal("traces client is disabled")
	}
	tracer := client.Tracer()
	if tracer == nil {
		t.Fatal("tracer is nil")
	}
	parent := tracer.StartSpan("parent", map[string]string{"rpc.method": "thread/start"})
	child := tracer.StartSpanWithParent(parent, "child", nil)
	if parent.TraceID != child.TraceID || child.ParentSpanID != parent.SpanID {
		t.Fatalf("child span ids = %#v parent=%#v", child, parent)
	}
	child.SetStatus(SpanStatusError, "boom")
	child.End()
	parent.End()

	if err := client.Flush(context.Background()); err != nil {
		t.Fatalf("Flush() error = %v", err)
	}
	if len(bodies) != 1 || contentTypes[0] != "application/json" {
		t.Fatalf("batches = %d content types = %#v", len(bodies), contentTypes)
	}
	resourceSpans := bodies[0]["resourceSpans"].([]any)
	batch := resourceSpans[0].(map[string]any)
	resource := batch["resource"].(map[string]any)
	if !strings.Contains(string(mustJSON(t, resource)), `"service.name"`) {
		t.Fatalf("resource = %#v", resource)
	}
	scopeSpans := batch["scopeSpans"].([]any)[0].(map[string]any)
	scope := scopeSpans["scope"].(map[string]any)
	// Rust names the tracer after the service name, so the exported
	// instrumentation scope is the service name.
	if scope["name"] != "codex-app-server" {
		t.Fatalf("scope = %#v", scope)
	}
	spans := scopeSpans["spans"].([]any)
	if len(spans) != 2 {
		t.Fatalf("exported spans = %d", len(spans))
	}
	// The batch span processor exports in completion order, so the child (it
	// ends first) precedes its parent.
	first := spanNamed(t, spans, "parent")
	if first["name"] != "parent" || first["traceId"] != parent.TraceID || first["spanId"] != parent.SpanID {
		t.Fatalf("parent span = %#v", first)
	}
	if first["startTimeUnixNano"] != "1700000000000000000" || first["endTimeUnixNano"] != "1700000000000000000" {
		t.Fatalf("parent timings = %#v", first)
	}
	attributes := map[string]string{}
	for _, attribute := range first["attributes"].([]any) {
		entry := attribute.(map[string]any)
		attributes[entry["key"].(string)] = entry["value"].(map[string]any)["stringValue"].(string)
	}
	if attributes["deployment"] != "test" || attributes["rpc.method"] != "thread/start" {
		t.Fatalf("parent attributes = %#v", attributes)
	}
	second := spanNamed(t, spans, "child")
	status := second["status"].(map[string]any)
	if second["name"] != "child" || second["parentSpanId"] != parent.SpanID ||
		status["code"] != float64(SpanStatusError) || status["message"] != "boom" {
		t.Fatalf("child span = %#v", second)
	}

	// A full queue drops spans instead of growing without bound.
	bounded := NewTracesClient(TracesClientOptions{Endpoint: server.URL, QueueSize: 1, ExportInterval: -1})
	boundedTracer := bounded.Tracer()
	for _, name := range []string{"a", "b"} {
		boundedTracer.StartSpan(name, nil).End()
	}
	if bounded.Dropped() != 1 {
		t.Fatalf("dropped = %d, want 1", bounded.Dropped())
	}

	// A provider with only the trace exporter builds the tracing pipeline alone.
	provider, err := NewOtelProvider(OtelSettings{
		ServiceName:   "codex-app-server",
		TraceExporter: OtelExporter{Kind: OtelExporterOtlpHTTP, Endpoint: server.URL + "/v1/traces"},
	})
	if err != nil {
		t.Fatalf("NewOtelProvider() error = %v", err)
	}
	if provider == nil || provider.Traces() == nil || provider.Tracer() == nil || provider.Metrics() != nil {
		t.Fatalf("provider = %#v", provider)
	}
	if err := provider.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}
	if provider.Tracer() == nil {
		t.Fatal("the tracer must survive shutdown")
	}
}

// The OTLP/HTTP binary protocol exports the official protobuf payload.
func TestTracesExporterBinaryProtocol(t *testing.T) {
	var contentType string
	var payload []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		contentType = r.Header.Get("Content-Type")
		payload, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	exporter := NewOTLPTracesExporter(OTLPTracesExporterOptions{
		Endpoint: server.URL + "/v1/traces",
		Protocol: OtelHTTPProtocolBinary,
		Timeout:  5 * time.Second,
	})
	if exporter == nil {
		t.Fatal("binary exporter is nil")
	}
	request := OTLPExportTracesRequest{ResourceSpans: []OTLPResourceSpans{{
		Resource: OTLPResource{Attributes: []MetricTagValue{{Key: "service.name", Value: "codex-app-server"}}},
		ScopeSpans: []OTLPScopeSpans{{
			Scope: OTLPScope{Name: TracesScopeName},
			Spans: []OTLPSpan{{
				TraceID:           "00112233445566778899aabbccddeeff",
				SpanID:            "0011223344556677",
				Name:              "app_server.request",
				Kind:              SpanKindServer,
				StartTimeUnixNano: "1700000000000000000",
				EndTimeUnixNano:   "1700000001000000000",
				Attributes:        []MetricTagValue{{Key: "rpc.method", Value: "thread/start"}},
				StatusCode:        SpanStatusOK,
			}},
		}},
	}}}
	if err := exporter.Export(context.Background(), request); err != nil {
		t.Fatalf("Export() error = %v", err)
	}
	if contentType != "application/x-protobuf" {
		t.Fatalf("content type = %q", contentType)
	}
	var decoded collectortracepb.ExportTraceServiceRequest
	if err := proto.Unmarshal(payload, &decoded); err != nil {
		t.Fatalf("protobuf decode error = %v", err)
	}
	spans := decoded.ResourceSpans[0].ScopeSpans[0].Spans
	if len(spans) != 1 || spans[0].Name != "app_server.request" ||
		spans[0].Kind.String() != "SPAN_KIND_SERVER" || spans[0].Status.GetCode().String() != "STATUS_CODE_OK" {
		t.Fatalf("decoded spans = %#v", spans)
	}
	if len(spans[0].TraceId) != 16 || len(spans[0].SpanId) != 8 {
		t.Fatalf("decoded ids = %#v", spans[0])
	}
}

// spanNamed returns the encoded span with the given name from one OTLP JSON
// batch, so ordering assumptions stay out of the assertions.
func spanNamed(t *testing.T, spans []any, name string) map[string]any {
	t.Helper()
	for _, entry := range spans {
		span, ok := entry.(map[string]any)
		if ok && span["name"] == name {
			return span
		}
	}
	t.Fatalf("span %q not found in %#v", name, spans)
	return nil
}
