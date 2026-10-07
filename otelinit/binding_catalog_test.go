package otelinit

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"codex_go/config"
	"codex_go/mcp"
	"codex_go/telemetry"
)

// Rust #51215 (`tool_catalog.rs` gates the catalog log half on
// `tracing::enabled!(target: "codex_otel.trace_safe", INFO)`): the host installs
// the catalog telemetry adapter at provider build, and the installed sink is the
// adapter that turns each measurement into an `mcp.binding_catalog` trace event.
func TestBuildProviderInstallsBindingCatalogTelemetryLikeRust(t *testing.T) {
	mcp.SetBindingCatalogTelemetryObserver(nil)
	t.Cleanup(func() { mcp.SetBindingCatalogTelemetryObserver(nil) })
	if mcp.BindingCatalogTelemetrySinkInstalled() {
		t.Fatal("a sink is installed before any host built a provider")
	}

	var bodies []map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		payload, _ := io.ReadAll(r.Body)
		var body map[string]any
		if err := json.Unmarshal(payload, &body); err != nil {
			t.Errorf("span batch json error = %v payload=%s", err, payload)
		}
		bodies = append(bodies, body)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	provider, err := BuildProvider(Options{Config: otlpTraceHTTPConfig(server.URL), ServiceName: "codex-app-server"})
	if err != nil || provider == nil {
		t.Fatalf("BuildProvider() provider=%#v err=%v", provider, err)
	}
	defer func() { _ = provider.Shutdown(t.Context()) }()
	if traces := provider.Traces(); traces == nil || !traces.Enabled() {
		t.Fatalf("trace pipeline = %#v, want an enabled client", traces)
	}
	if !mcp.BindingCatalogTelemetrySinkInstalled() {
		t.Fatal("BuildProvider did not install the binding catalog telemetry sink")
	}
	if installed, want := InstallBindingCatalogTelemetry(provider), BindingCatalogTelemetryObserver(); reflect.ValueOf(installed).Pointer() != reflect.ValueOf(want).Pointer() {
		t.Fatal("the host installs a different sink than BindingCatalogTelemetryObserver")
	}

	// The adapter turns one measurement into the trace event, on the emitting
	// thread's live span.
	span := provider.Tracer().StartSpan("session_loop", map[string]string{telemetry.ThreadIDAttribute: "thread-1"})
	if span == nil {
		t.Fatal("session span is nil")
	}
	BindingCatalogTelemetryObserver()([]mcp.BindingCatalogTelemetry{{
		ThreadID:                "thread-1",
		ServerName:              "plugged",
		ProductSKU:              "codex",
		ServerKind:              mcp.BindingCatalogServerKindPlugin,
		PluginID:                "plug-1",
		CatalogSource:           mcp.BindingCatalogSourceLive,
		ToolCount:               3,
		ToolDefinitionJSONBytes: 4096,
	}})
	span.End()
	if err := provider.Traces().Flush(t.Context()); err != nil {
		t.Fatalf("Flush() error = %v", err)
	}
	if len(bodies) != 1 {
		t.Fatalf("span batches = %d, want 1", len(bodies))
	}
	attributeNames := exportedEventAttributes(t, bodies[0])
	for key, want := range map[string]string{
		"target":                     telemetry.OtelTraceSafeTarget,
		"event.name":                 telemetry.BindingCatalogEventName,
		"product_sku":                "codex",
		"server_kind":                mcp.BindingCatalogServerKindPlugin,
		"plugin_id":                  "plug-1",
		"catalog_source":             mcp.BindingCatalogSourceLive,
		"tool_count":                 "3",
		"tool_definition_json_bytes": "4096",
	} {
		if got := attributeNames[key]; got != want {
			t.Fatalf("event attribute %q = %q, want %q (attributes=%v)", key, got, want, attributeNames)
		}
	}
}

// Without an enabled trace pipeline the events would be measured and then
// dropped, so the host leaves the sink unset (Rust's disabled trace-safe target).
func TestInstallBindingCatalogTelemetrySkipsDisabledTracesLikeRust(t *testing.T) {
	mcp.SetBindingCatalogTelemetryObserver(nil)
	t.Cleanup(func() { mcp.SetBindingCatalogTelemetryObserver(nil) })

	if got := InstallBindingCatalogTelemetry(nil); got != nil {
		t.Fatalf("InstallBindingCatalogTelemetry(nil) = %#v, want nil", got)
	}
	provider, err := BuildProvider(Options{Config: otlpHTTPConfig(), ServiceName: "codex-app-server"})
	if err != nil {
		t.Fatalf("BuildProvider() error = %v", err)
	}
	if provider != nil {
		defer func() { _ = provider.Shutdown(t.Context()) }()
		if traces := provider.Traces(); traces != nil && traces.Enabled() {
			t.Fatalf("metrics-only config built an enabled trace pipeline: %#v", traces)
		}
	}
	if mcp.BindingCatalogTelemetrySinkInstalled() {
		t.Fatal("the host installed the catalog telemetry sink without a trace pipeline")
	}
	if got := InstallBindingCatalogTelemetry(provider); got != nil {
		t.Fatalf("InstallBindingCatalogTelemetry(disabled) = %#v, want nil", got)
	}
}

// otlpTraceHTTPConfig is otlpHTTPConfig plus an OTLP/HTTP trace exporter, the
// host configuration that enables the trace-safe target.
func otlpTraceHTTPConfig(endpoint string) *config.Config {
	cfg := otlpHTTPConfig()
	otel := cfg.Values["otel"].(map[string]any)
	otel["trace_exporter"] = map[string]any{
		"otlp-http": map[string]any{
			"endpoint": endpoint + "/v1/traces",
			"protocol": config.OtelHTTPProtocolJSON,
		},
	}
	return cfg
}

func exportedEventAttributes(t *testing.T, body map[string]any) map[string]string {
	t.Helper()
	resourceSpans, _ := body["resourceSpans"].([]any)
	if len(resourceSpans) != 1 {
		t.Fatalf("resourceSpans = %#v", body["resourceSpans"])
	}
	scopeSpans, _ := resourceSpans[0].(map[string]any)["scopeSpans"].([]any)
	if len(scopeSpans) != 1 {
		t.Fatalf("scopeSpans = %#v", resourceSpans[0])
	}
	spans, _ := scopeSpans[0].(map[string]any)["spans"].([]any)
	if len(spans) != 1 {
		t.Fatalf("spans = %#v", scopeSpans[0])
	}
	events, _ := spans[0].(map[string]any)["events"].([]any)
	if len(events) != 1 {
		t.Fatalf("span events = %#v, want the catalog record", spans[0].(map[string]any)["events"])
	}
	attributes := map[string]string{}
	for _, entry := range events[0].(map[string]any)["attributes"].([]any) {
		attribute, _ := entry.(map[string]any)
		value, _ := attribute["value"].(map[string]any)
		text, _ := value["stringValue"].(string)
		key, _ := attribute["key"].(string)
		attributes[key] = text
	}
	return attributes
}
