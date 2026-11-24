package appserver

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"codex_go/config"
	"codex_go/state"
	"codex_go/telemetry"
)

// Rust #63475: the cloud catalog refresh records the mutex wait in its own
// `cloud_catalog.refresh.acquire_lock` span and marks `acquired` once the lock
// is held, so the wait is visible separately from discovery work.
func TestCloudCatalogRefreshLockSpanLikeRust(t *testing.T) {
	traceBodies := make(chan map[string]any, 8)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		payload := map[string]any{}
		_ = json.NewDecoder(request.Body).Decode(&payload)
		select {
		case traceBodies <- payload:
		default:
		}
		writer.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	provider, err := telemetry.NewOtelProvider(telemetry.OtelSettings{
		ServiceName:   otelAppServerServiceName,
		TraceExporter: telemetry.OtelExporter{Kind: telemetry.OtelExporterOtlpHTTP, Endpoint: server.URL + "/v1/traces"},
	})
	if err != nil || provider == nil {
		t.Fatalf("provider = %#v error = %v", provider, err)
	}
	router := newCloudCatalogTestRouter(t, &countingPluginCatalogProvider{})
	router.installOtelProvider(provider, state.NewTaskMetrics())
	defer router.Close()

	router.refreshCloudPluginCatalogForStep(context.Background(), &config.Config{}, "thread-lock", "turn-1")
	if err := provider.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}

	var bodies []map[string]any
	for drained := false; !drained; {
		select {
		case body := <-traceBodies:
			bodies = append(bodies, body)
		default:
			drained = true
		}
	}
	for _, span := range exportedSpans(t, bodies) {
		if span["name"] != cloudCatalogRefreshAcquireLockSpanName {
			continue
		}
		if acquired, _ := encodedAttributes(span)["acquired"]; acquired != "true" {
			t.Fatalf("acquired = %q, want true", acquired)
		}
		return
	}
	t.Fatalf("cloud_catalog.refresh.acquire_lock span was not exported: %#v", bodies)
}
