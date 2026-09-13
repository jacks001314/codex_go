package appserver

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"codex_go/config"
	"codex_go/state"
)

// The app-server builds the OTEL provider from config and forwards the task
// metrics to its metrics client: a configured OTLP/HTTP exporter with analytics
// enabled receives the recorded metric when the router shuts the provider down.
func TestConfigureOtelMetricsForwardsTaskMetrics(t *testing.T) {
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
		"[analytics]\nenabled = true\n\n[otel.metrics_exporter.otlp-http]\nendpoint = %q\nprotocol = \"json\"\n",
		server.URL+"/v1/metrics",
	)
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(configToml), 0o600); err != nil {
		t.Fatalf("WriteFile config.toml error = %v", err)
	}

	router := NewRuntimeRouter(RuntimeServices{Config: config.NewConfigService(home)})
	metrics := state.NewTaskMetrics()
	router.configureOtelMetrics(home, nil, metrics)
	if router.otelProvider == nil || router.otelProvider.Metrics() == nil {
		t.Fatal("the OTLP metrics provider was not built")
	}
	metrics.Counter("codex.thread.started", 1, nil)
	if err := router.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	select {
	case payload := <-received:
		resourceMetrics, ok := payload["resourceMetrics"].([]any)
		if !ok || len(resourceMetrics) == 0 {
			t.Fatalf("payload = %#v", payload)
		}
		resource := resourceMetrics[0].(map[string]any)["resource"].(map[string]any)
		if name := otelAttributeValue(resource["attributes"], "service.name"); name != otelAppServerServiceName {
			t.Fatalf("service.name = %q", name)
		}
		if !payloadHasMetric(payload, "codex.thread.started") {
			t.Fatalf("payload = %#v", payload)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the OTLP endpoint did not receive an export")
	}
}

// Without a configured OTLP exporter (the default Statsig route is inert in
// development builds) no provider is built and no metrics are exported.
func TestConfigureOtelMetricsDefaultsToNoProvider(t *testing.T) {
	home := t.TempDir()
	router := NewRuntimeRouter(RuntimeServices{Config: config.NewConfigService(home)})
	metrics := state.NewTaskMetrics()
	router.configureOtelMetrics(home, nil, metrics)
	if router.otelProvider != nil {
		t.Fatalf("provider = %#v", router.otelProvider)
	}
	// Recording still works and stays local.
	metrics.Counter("codex.thread.started", 1, nil)
	if records := metrics.Records(); len(records) != 1 {
		t.Fatalf("records = %#v", records)
	}
	if err := router.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
}

func otelAttributeValue(raw any, key string) string {
	attributes, ok := raw.([]any)
	if !ok {
		return ""
	}
	for _, entry := range attributes {
		attribute, ok := entry.(map[string]any)
		if !ok || attribute["key"] != key {
			continue
		}
		if value, ok := attribute["value"].(map[string]any); ok {
			text, _ := value["stringValue"].(string)
			return text
		}
	}
	return ""
}

func payloadHasMetric(payload map[string]any, name string) bool {
	resourceMetrics, ok := payload["resourceMetrics"].([]any)
	if !ok || len(resourceMetrics) == 0 {
		return false
	}
	scopeMetrics, ok := resourceMetrics[0].(map[string]any)["scopeMetrics"].([]any)
	if !ok || len(scopeMetrics) == 0 {
		return false
	}
	metrics, ok := scopeMetrics[0].(map[string]any)["metrics"].([]any)
	if !ok {
		return false
	}
	for _, entry := range metrics {
		if metric, ok := entry.(map[string]any); ok && metric["name"] == name {
			return true
		}
	}
	return false
}
