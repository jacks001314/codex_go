package appserver

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"codex_go/config"
	"codex_go/state"
)

// otlpMetricsCapture records every OTLP/HTTP metrics batch it receives.
type otlpMetricsCapture struct {
	mu       sync.Mutex
	payloads []map[string]any
	server   *httptest.Server
}

func newOTLPMetricsCapture() *otlpMetricsCapture {
	capture := &otlpMetricsCapture{}
	capture.server = httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		payload := map[string]any{}
		_ = json.NewDecoder(request.Body).Decode(&payload)
		capture.mu.Lock()
		capture.payloads = append(capture.payloads, payload)
		capture.mu.Unlock()
		writer.WriteHeader(http.StatusOK)
	}))
	return capture
}

func (c *otlpMetricsCapture) close() {
	c.server.Close()
}

func (c *otlpMetricsCapture) waitForMetric(name string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for {
		if c.receivedMetric(name) {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func (c *otlpMetricsCapture) receivedMetric(name string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, payload := range c.payloads {
		if payloadHasMetric(payload, name) {
			return true
		}
	}
	return false
}

func writeOtlpMetricsConfig(t *testing.T, home string, endpoint string) {
	t.Helper()
	configToml := fmt.Sprintf(
		"[analytics]\nenabled = true\n\n[otel.metrics_exporter.otlp-http]\nendpoint = %q\nprotocol = \"json\"\n",
		endpoint,
	)
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(configToml), 0o600); err != nil {
		t.Fatalf("WriteFile config.toml error = %v", err)
	}
}

// Rust's otel_reloader::spawn rebuilds the exporters after an auth change: the
// new provider takes over and the previous one is shut down, so metrics
// recorded after the change reach the newly configured collector.
func TestOtelProviderReloadsAfterAccountChange(t *testing.T) {
	first := newOTLPMetricsCapture()
	defer first.close()
	second := newOTLPMetricsCapture()
	defer second.close()

	home := t.TempDir()
	writeOtlpMetricsConfig(t, home, first.server.URL+"/v1/metrics")
	router := NewRuntimeRouter(RuntimeServices{Config: config.NewConfigService(home)})
	metrics := state.NewTaskMetrics()
	router.configureOtelMetrics(home, nil, metrics)
	initial := router.currentOtelProvider()
	if initial == nil {
		t.Fatal("the OTLP metrics provider was not built")
	}

	metrics.Counter("codex.before_reload", 1, nil)
	writeOtlpMetricsConfig(t, home, second.server.URL+"/v1/metrics")
	router.noteAuthChanged()

	deadline := time.Now().Add(5 * time.Second)
	for router.currentOtelProvider() == initial {
		if time.Now().After(deadline) {
			t.Fatal("the telemetry exporters were not rebuilt after the account change")
		}
		time.Sleep(10 * time.Millisecond)
	}
	metrics.Counter("codex.after_reload", 1, nil)
	if err := router.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	if !first.waitForMetric("codex.before_reload", 3*time.Second) {
		t.Fatal("the pre-reload collector did not receive the earlier metric")
	}
	if !second.waitForMetric("codex.after_reload", 3*time.Second) {
		t.Fatal("the reloaded collector did not receive the later metric")
	}
}

// Closing the router stops the reloader, so a later auth change must not rebuild
// exporters that were already shut down.
func TestOtelReloaderStopsAtClose(t *testing.T) {
	home := t.TempDir()
	writeOtlpMetricsConfig(t, home, "http://127.0.0.1:1/v1/metrics")
	router := NewRuntimeRouter(RuntimeServices{Config: config.NewConfigService(home)})
	metrics := state.NewTaskMetrics()
	router.configureOtelMetrics(home, nil, metrics)
	if err := router.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	router.otelReloadMu.Lock()
	stopped := router.otelReloadStop == nil
	router.otelReloadMu.Unlock()
	if !stopped {
		t.Fatal("the reloader was not stopped at close")
	}
	// A later change must stay a no-op instead of blocking on a dead reloader.
	router.reloadOtelProvider(home, nil, metrics)
}
