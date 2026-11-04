package appserver

import (
	"context"
	"reflect"
	"testing"
	"time"

	"codex_go/turn"
)

type modelSearchMetricRecord struct {
	name string
	tags map[string]string
}

type recordingModelSearchMetrics struct {
	records []modelSearchMetricRecord
}

func modelSearchMetricTags(tags map[string]string) map[string]string {
	if tags == nil {
		return nil
	}
	out := make(map[string]string, len(tags))
	for key, value := range tags {
		out[key] = value
	}
	return out
}

func (m *recordingModelSearchMetrics) Counter(name string, inc int, tags map[string]string) {
	for i := 0; i < inc; i++ {
		m.records = append(m.records, modelSearchMetricRecord{name: name, tags: modelSearchMetricTags(tags)})
	}
}

func (m *recordingModelSearchMetrics) Histogram(string, int, map[string]string) {}
func (m *recordingModelSearchMetrics) HistogramWithBounds(string, int, []float64, map[string]string) {
}
func (m *recordingModelSearchMetrics) RecordDuration(string, time.Duration, map[string]string) {}

// Rust's SearchTurnUsage records the first use of each provider in a turn and
// one mixed count when the turn used both providers (#59036).
func TestModelSearchTurnUsageEmitsProviderCountersLikeRust(t *testing.T) {
	metrics := &recordingModelSearchMetrics{}
	router := NewRuntimeRouter(RuntimeServices{TurnMetrics: metrics})
	router.recordModelSearchUse("thread-1", "turn-1", modelSearchClientMCP)
	router.recordModelSearchUse("thread-1", "turn-1", modelSearchHosted)
	router.recordModelSearchUse("thread-1", "turn-1", modelSearchNative)
	router.recordModelSearchUse("thread-1", "turn-1", modelSearchNative)

	want := []modelSearchMetricRecord{
		{name: "codex.model_search.turn", tags: map[string]string{"provider": "search_mcp"}},
		{name: "codex.model_search.turn", tags: map[string]string{"provider": "native"}},
		{name: "codex.model_search.turn", tags: map[string]string{"provider": "mixed"}},
	}
	if !reflect.DeepEqual(metrics.records, want) {
		t.Fatalf("turn counters = %#v, want %#v", metrics.records, want)
	}
}

// Every resolution reports its decision reason, including the legacy
// unmanaged case, like Rust's observe/finish closures (#59036).
func TestModelSearchDiscoveryEmitsReasonCounterLikeRust(t *testing.T) {
	metrics := &recordingModelSearchMetrics{}
	router := NewRuntimeRouter(RuntimeServices{TurnMetrics: metrics})
	cfg := modelSearchTestConfig(map[string]any{"web_search": "live"})
	selection := router.resolveModelSearchForTurn(context.Background(), "thread-1", "turn-1", cfg, modelSearchTestModel("standalone"), nil, nil, &turn.WebSearchOptions{}, nil)
	if selection.provider != modelSearchNative {
		t.Fatalf("provider = %q, want native", selection.provider)
	}
	want := []modelSearchMetricRecord{
		{name: "codex.model_search.discovery", tags: map[string]string{"reason": "native_selected"}},
	}
	if !reflect.DeepEqual(metrics.records, want) {
		t.Fatalf("discovery counters = %#v, want %#v", metrics.records, want)
	}
}
