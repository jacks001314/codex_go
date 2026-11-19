package appserver

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"codex_go/metrics"
	"codex_go/plugin"
	"codex_go/telemetry"
	"codex_go/tracehook"
)

// recommendationLatencyRecorder captures the initial-context recommendation
// wait metric and ignores every other series.
type recommendationLatencyRecorder struct {
	mu        sync.Mutex
	durations []time.Duration
}

func (r *recommendationLatencyRecorder) RecordDuration(name string, duration time.Duration, _ map[string]string) {
	if name != metrics.PluginRecommendationsWaitMetric {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.durations = append(r.durations, duration)
}

func (r *recommendationLatencyRecorder) Counter(string, int, map[string]string)   {}
func (r *recommendationLatencyRecorder) Histogram(string, int, map[string]string) {}
func (r *recommendationLatencyRecorder) recorded() []time.Duration {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]time.Duration(nil), r.durations...)
}

// Mirrors Rust #62326: preparing a turn records how long initial context setup
// waited for the plugin recommendation catalog.
func TestTurnPreparationRecordsPluginRecommendationsWaitLikeRust(t *testing.T) {
	recorder := &recommendationLatencyRecorder{}
	previous := metrics.InstallGlobal(recorder)
	defer metrics.InstallGlobal(previous)

	router := NewRuntimeRouter(RuntimeServices{Plugins: plugin.NewPluginService()})
	if _, _, _, _, err := router.prepareTurnToolInputs(context.Background(), "thread-recommendations", nil, nil, nil, nil); err != nil {
		t.Fatalf("prepareTurnToolInputs() error = %v", err)
	}
	durations := recorder.recorded()
	if len(durations) != 1 {
		t.Fatalf("recommendation wait samples = %d, want 1", len(durations))
	}
	if durations[0] < 0 {
		t.Fatalf("recommendation wait = %s", durations[0])
	}
}

// Mirrors Rust #62326's span names: the load span and its two remote reads are
// exported under the Rust names, nested so a slow endpoint is attributable.
func TestPluginRecommendationSpansUseRustNamesLikeRust(t *testing.T) {
	received := make(chan map[string]any, 4)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		payload, _ := io.ReadAll(request.Body)
		var body map[string]any
		if err := json.Unmarshal(payload, &body); err != nil {
			t.Errorf("span batch json error = %v payload=%s", err, payload)
		}
		select {
		case received <- body:
		default:
		}
		writer.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	traces := telemetry.NewTracesClient(telemetry.TracesClientOptions{
		ServiceName:    "codex-app-server",
		Endpoint:       server.URL + "/v1/traces",
		ExportInterval: -1,
	})
	ctx, loadSpan := startRecommendationsLoadSpan(traces.Tracer(), context.Background())
	if loadSpan == nil {
		t.Fatal("startRecommendationsLoadSpan() opened no span")
	}
	// The remote reads nest under the load span through the tracehook bridge.
	if span := tracehook.SpanFromContext(ctx); span == nil {
		t.Fatal("the load context carries no tracehook span")
	}
	markerSpan := tracehook.SpanFromContext(ctx).StartChildSpan(plugin.RecommendationsReadToolingMarkerSpanName, nil)
	markerSpan.End()
	catalogSpan := tracehook.SpanFromContext(ctx).StartChildSpan(plugin.RecommendationsFetchCatalogSpanName, nil)
	catalogSpan.End()
	loadSpan.End()
	if err := traces.Flush(context.Background()); err != nil {
		t.Fatalf("Flush() error = %v", err)
	}
	names := map[string]bool{}
	deadline := time.After(3 * time.Second)
	for len(names) < 3 {
		select {
		case body := <-received:
			for _, name := range exportedSpanNames(body) {
				names[name] = true
			}
		case <-deadline:
			t.Fatalf("exported span names = %#v", names)
		}
	}
	for _, want := range []string{
		plugin.RecommendationsLoadSpanName,
		plugin.RecommendationsReadToolingMarkerSpanName,
		plugin.RecommendationsFetchCatalogSpanName,
	} {
		if !names[want] {
			t.Fatalf("exported span names = %#v, want %q", names, want)
		}
	}
}

// exportedSpanNames lists the span names of one OTLP span batch.
func exportedSpanNames(body map[string]any) []string {
	var names []string
	for _, resource := range listOfMaps(body["resourceSpans"]) {
		for _, scope := range listOfMaps(resource["scopeSpans"]) {
			for _, span := range listOfMaps(scope["spans"]) {
				if name, ok := span["name"].(string); ok {
					names = append(names, name)
				}
			}
		}
	}
	return names
}
