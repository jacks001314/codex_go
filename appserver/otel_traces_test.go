package appserver

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"codex_go/config"
	"codex_go/session"
	"codex_go/state"
	"codex_go/telemetry"
)

// Rust's app_server_tracing.rs::app_server_request_span_template turns every
// transport request into a `server` span named after the RPC method and
// carrying the RPC and connection identity.
func TestRouterRequestSpanMatchesRust(t *testing.T) {
	var batches []map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		payload := map[string]any{}
		_ = json.NewDecoder(request.Body).Decode(&payload)
		batches = append(batches, payload)
		writer.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := telemetry.NewTracesClient(telemetry.TracesClientOptions{
		ServiceName:    otelAppServerServiceName,
		Endpoint:       server.URL + "/v1/traces",
		ExportInterval: -1,
	})
	router := NewRouter(session.NewStore(t.TempDir()))
	router.SetTracer(client.Tracer())
	router.SetRequestTransport("stdio")
	router.Handle(requestWithParams(t, IntID(7), MethodThreadList, ThreadListParams{}))
	if err := client.Flush(context.Background()); err != nil {
		t.Fatalf("Flush() error = %v", err)
	}

	spans := exportedSpans(t, batches)
	if len(spans) != 1 {
		t.Fatalf("exported spans = %d", len(spans))
	}
	span := spans[0]
	if span["name"] != string(MethodThreadList) {
		t.Fatalf("span name = %#v", span["name"])
	}
	if span["kind"] != float64(telemetry.SpanKindServer) {
		t.Fatalf("span kind = %#v", span["kind"])
	}
	for key, want := range map[string]string{
		"rpc.system":               "jsonrpc",
		"rpc.method":               string(MethodThreadList),
		"rpc.transport":            "stdio",
		"rpc.request_id":           "7",
		"app_server.connection_id": "default",
		"app_server.api_version":   "v2",
	} {
		if got := otelAttributeValue(span["attributes"], key); got != want {
			t.Fatalf("attribute %s = %q, want %q", key, got, want)
		}
	}
}

// A router used in process keeps Rust's "in-process" transport stamp instead of
// inheriting a transport it does not serve.
func TestRouterRequestSpanDefaultsToInProcessTransport(t *testing.T) {
	var batches []map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		payload := map[string]any{}
		_ = json.NewDecoder(request.Body).Decode(&payload)
		batches = append(batches, payload)
		writer.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := telemetry.NewTracesClient(telemetry.TracesClientOptions{
		ServiceName:    otelAppServerServiceName,
		Endpoint:       server.URL + "/v1/traces",
		ExportInterval: -1,
	})
	router := NewRouter(session.NewStore(t.TempDir()))
	router.SetTracer(client.Tracer())
	router.Handle(requestWithParams(t, IntID(1), MethodThreadList, ThreadListParams{}))
	if err := client.Flush(context.Background()); err != nil {
		t.Fatalf("Flush() error = %v", err)
	}

	spans := exportedSpans(t, batches)
	if len(spans) != 1 {
		t.Fatalf("exported spans = %d", len(spans))
	}
	if got := otelAttributeValue(spans[0]["attributes"], "rpc.transport"); got != "in-process" {
		t.Fatalf("rpc.transport = %q", got)
	}
}

// The app-server installs the provider's tracer on its request router, so a
// configured trace exporter receives the request span on shutdown.
func TestConfigureOtelMetricsInstallsRequestTracer(t *testing.T) {
	received := make(chan map[string]any, 1)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		payload := map[string]any{}
		_ = json.NewDecoder(request.Body).Decode(&payload)
		select {
		case received <- payload:
		default:
		}
		writer.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	home := t.TempDir()
	configToml := fmt.Sprintf(
		"[otel.trace_exporter.otlp-http]\nendpoint = %q\nprotocol = \"json\"\n",
		server.URL+"/v1/traces",
	)
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(configToml), 0o600); err != nil {
		t.Fatalf("WriteFile config.toml error = %v", err)
	}

	router := NewRuntimeRouter(RuntimeServices{
		Config:       config.NewConfigService(home),
		ThreadRouter: NewRouter(session.NewStore(t.TempDir())),
	})
	metrics := state.NewTaskMetrics()
	router.configureOtelMetrics(home, nil, metrics)
	if router.otelProvider == nil || router.otelProvider.Tracer() == nil {
		t.Fatal("the OTLP trace provider was not built")
	}
	router.Handle(requestWithParams(t, IntID(1), MethodThreadList, ThreadListParams{}))
	if err := router.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	select {
	case payload := <-received:
		spans := exportedSpans(t, []map[string]any{payload})
		if len(spans) != 1 || spans[0]["name"] != string(MethodThreadList) {
			t.Fatalf("exported spans = %#v", spans)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the OTLP endpoint did not receive the request span")
	}
}

// An inbound request trace carrier becomes the request span's parent, so the
// exported span stays inside the client's trace (Rust's attach_parent_context).
func TestRouterRequestSpanContinuesInboundTrace(t *testing.T) {
	var batches []map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		payload := map[string]any{}
		_ = json.NewDecoder(request.Body).Decode(&payload)
		batches = append(batches, payload)
		writer.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := telemetry.NewTracesClient(telemetry.TracesClientOptions{
		ServiceName:    otelAppServerServiceName,
		Endpoint:       server.URL + "/v1/traces",
		ExportInterval: -1,
	})
	router := NewRouter(session.NewStore(t.TempDir()))
	router.SetTracer(client.Tracer())
	request := requestWithParams(t, IntID(1), MethodThreadList, ThreadListParams{})
	request.Trace = &W3CTraceContext{
		Traceparent: "00-00000000000000000000000000000001-0000000000000002-01",
		Tracestate:  "other=value",
	}
	router.Handle(request)
	if err := client.Flush(context.Background()); err != nil {
		t.Fatalf("Flush() error = %v", err)
	}

	spans := exportedSpans(t, batches)
	if len(spans) != 1 {
		t.Fatalf("exported spans = %d", len(spans))
	}
	span := spans[0]
	if span["traceId"] != "00000000000000000000000000000001" ||
		span["parentSpanId"] != "0000000000000002" {
		t.Fatalf("span = %#v", span)
	}
	if span["traceState"] != "other=value" {
		t.Fatalf("traceState = %#v", span["traceState"])
	}
}

// An unusable trace carrier is ignored instead of failing the request: the span
// stays a root span, like Rust's "ignoring invalid inbound request trace
// carrier" warning path.
func TestRouterRequestSpanIgnoresInvalidTraceCarrier(t *testing.T) {
	var batches []map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		payload := map[string]any{}
		_ = json.NewDecoder(request.Body).Decode(&payload)
		batches = append(batches, payload)
		writer.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := telemetry.NewTracesClient(telemetry.TracesClientOptions{
		ServiceName:    otelAppServerServiceName,
		Endpoint:       server.URL + "/v1/traces",
		ExportInterval: -1,
	})
	router := NewRouter(session.NewStore(t.TempDir()))
	router.SetTracer(client.Tracer())
	request := requestWithParams(t, IntID(1), MethodThreadList, ThreadListParams{})
	request.Trace = &W3CTraceContext{Traceparent: "not-a-traceparent"}
	if response := router.Handle(request); response == nil || response.Error != nil {
		t.Fatalf("response = %#v", response)
	}
	if err := client.Flush(context.Background()); err != nil {
		t.Fatalf("Flush() error = %v", err)
	}

	spans := exportedSpans(t, batches)
	if len(spans) != 1 {
		t.Fatalf("exported spans = %d", len(spans))
	}
	if spans[0]["parentSpanId"] != nil {
		t.Fatalf("span = %#v", spans[0])
	}
}

// The request's optional `trace` carrier round trips through JSON like Rust's
// JSONRPCRequest::trace.
func TestRequestTraceCarrierRoundTripsLikeRust(t *testing.T) {
	raw := []byte(`{"id":1,"method":"thread/list","params":{},"trace":{"traceparent":"00-00000000000000000000000000000001-0000000000000002-01","tracestate":"other=value"}}`)
	var request Request
	if err := json.Unmarshal(raw, &request); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if request.Trace == nil || request.Trace.Traceparent != "00-00000000000000000000000000000001-0000000000000002-01" ||
		request.Trace.Tracestate != "other=value" {
		t.Fatalf("request.Trace = %#v", request.Trace)
	}
	encoded, err := json.Marshal(request)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	if !strings.Contains(string(encoded), `"tracestate":"other=value"`) {
		t.Fatalf("encoded request = %s", encoded)
	}
}

// exportedSpans flattens the resource/scope nesting of one captured OTLP/HTTP
// trace export.
func exportedSpans(t *testing.T, batches []map[string]any) []map[string]any {
	t.Helper()
	spans := []map[string]any{}
	for _, batch := range batches {
		resourceSpans, _ := batch["resourceSpans"].([]any)
		for _, entry := range resourceSpans {
			resource, _ := entry.(map[string]any)
			scopeSpans, _ := resource["scopeSpans"].([]any)
			for _, scopeEntry := range scopeSpans {
				scoped, _ := scopeEntry.(map[string]any)
				rawSpans, _ := scoped["spans"].([]any)
				for _, rawSpan := range rawSpans {
					span, ok := rawSpan.(map[string]any)
					if ok {
						spans = append(spans, span)
					}
				}
			}
		}
	}
	return spans
}
