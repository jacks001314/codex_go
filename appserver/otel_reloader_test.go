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

// otlpTraceCapture records every OTLP/HTTP trace batch it receives.
type otlpTraceCapture struct {
	mu       sync.Mutex
	payloads []map[string]any
	server   *httptest.Server
}

func newOTLPTraceCapture() *otlpTraceCapture {
	capture := &otlpTraceCapture{}
	capture.server = httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v1/traces" {
			writer.WriteHeader(http.StatusOK)
			return
		}
		payload := map[string]any{}
		_ = json.NewDecoder(request.Body).Decode(&payload)
		capture.mu.Lock()
		capture.payloads = append(capture.payloads, payload)
		capture.mu.Unlock()
		writer.WriteHeader(http.StatusOK)
	}))
	return capture
}

func (c *otlpTraceCapture) close() {
	c.server.Close()
}

func (c *otlpTraceCapture) batches() []map[string]any {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]map[string]any(nil), c.payloads...)
}

func writeOtlpTraceConfig(t *testing.T, home string, endpoint string) {
	t.Helper()
	configToml := fmt.Sprintf(
		"[otel.trace_exporter.otlp-http]\nendpoint = %q\nprotocol = \"json\"\n",
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

	// Exporters rebuild on a background goroutine; a loaded Windows host can
	// take several seconds to tear down and recreate the OTLP providers.
	deadline := time.Now().Add(45 * time.Second)
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

	if !first.waitForMetric("codex.before_reload", 20*time.Second) {
		t.Fatal("the pre-reload collector did not receive the earlier metric")
	}
	if !second.waitForMetric("codex.after_reload", 20*time.Second) {
		t.Fatal("the reloaded collector did not receive the later metric")
	}
}

// Rust #57657: starting with tracing disabled must not prevent an account
// switch from installing a trace exporter, and the exported request span must
// keep the inbound parent trace.
func TestOtelProviderActivatesTraceExporterAfterAccountChangeLikeRust(t *testing.T) {
	capture := newOTLPTraceCapture()
	defer capture.close()

	home := t.TempDir()
	writeOtlpMetricsConfig(t, home, capture.server.URL+"/v1/metrics")
	router := NewRuntimeRouter(RuntimeServices{Config: config.NewConfigService(home)})
	router.configureOtelMetrics(home, nil, state.NewTaskMetrics())
	initial := router.currentOtelProvider()
	if initial == nil {
		t.Fatal("the initial metrics provider was not built")
	}
	if tracer := initial.Tracer(); tracer != nil {
		t.Fatal("the initial provider installed a tracer while tracing was disabled")
	}

	// A request handled while tracing is off must not be exported later.
	router.Handle(requestWithParams(t, IntID(1), MethodThreadList, ThreadListParams{}))

	writeOtlpTraceConfig(t, home, capture.server.URL+"/v1/traces")
	router.noteAuthChanged()
	deadline := time.Now().Add(45 * time.Second)
	for {
		provider := router.currentOtelProvider()
		if provider != nil && provider != initial && provider.Tracer() != nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the trace exporter was not activated after the account change")
		}
		time.Sleep(10 * time.Millisecond)
	}

	request := requestWithParams(t, IntID(2), MethodThreadList, ThreadListParams{})
	request.Trace = &W3CTraceContext{
		Traceparent: "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01",
		Tracestate:  "other=value",
	}
	router.Handle(request)
	if err := router.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	spans := exportedSpans(t, capture.batches())
	if len(spans) != 1 {
		t.Fatalf("exported spans = %d, want the request handled after tracing was enabled", len(spans))
	}
	span := spans[0]
	if span["traceId"] != "4bf92f3577b34da6a3ce929d0e0e4736" ||
		span["parentSpanId"] != "00f067aa0ba902b7" {
		t.Fatalf("span = %#v, want the inbound parent trace", span)
	}
	if span["name"] != string(MethodThreadList) {
		t.Fatalf("span name = %#v", span["name"])
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
