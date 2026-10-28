package appserver

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"codex_go/session"
	"codex_go/state"
	"codex_go/telemetry"
)

// TestInstallOtelProviderClearsTracerWhenTracingDisabledLikeRust covers Rust
// #57657: a reload that resolves to no provider installs no exporter, so the
// previous request tracer must be removed instead of recording spans forever.
func TestInstallOtelProviderClearsTracerWhenTracingDisabledLikeRust(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	provider, err := telemetry.NewOtelProvider(telemetry.OtelSettings{
		ServiceName:   otelAppServerServiceName,
		TraceExporter: telemetry.OtelExporter{Kind: telemetry.OtelExporterOtlpHTTP, Endpoint: server.URL + "/v1/traces"},
	})
	if err != nil || provider == nil {
		t.Fatalf("NewOtelProvider() = %#v, error = %v", provider, err)
	}
	defer func() { _ = provider.Shutdown(t.Context()) }()

	router := NewRuntimeRouter(RuntimeServices{ThreadRouter: NewRouter(session.NewStore(t.TempDir()))})
	router.installOtelProvider(provider, state.NewTaskMetrics())
	if router.services.ThreadRouter.tracer == nil {
		t.Fatal("installOtelProvider() did not install the provider tracer")
	}

	router.installOtelProvider(nil, state.NewTaskMetrics())
	if router.services.ThreadRouter.tracer != nil {
		t.Fatal("installOtelProvider(nil) left the previous tracer installed")
	}
}
