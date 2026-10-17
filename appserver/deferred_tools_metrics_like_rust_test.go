package appserver

import (
	"sync"
	"testing"
	"time"

	contextfrag "codex_go/context"
	"codex_go/telemetry"
)

type deferredToolsHistogramSample struct {
	name       string
	value      int
	boundaries []float64
	tags       map[string]string
}

type deferredToolsMetricSinkForTest struct {
	mu      sync.Mutex
	samples []deferredToolsHistogramSample
}

func (s *deferredToolsMetricSinkForTest) Counter(string, int, map[string]string) {}

func (s *deferredToolsMetricSinkForTest) Histogram(name string, value int, tags map[string]string) {
	s.HistogramWithBounds(name, value, nil, tags)
}

func (s *deferredToolsMetricSinkForTest) HistogramWithBounds(name string, value int, boundaries []float64, tags map[string]string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cloned := map[string]string{}
	for key, value := range tags {
		cloned[key] = value
	}
	s.samples = append(s.samples, deferredToolsHistogramSample{
		name: name, value: value, boundaries: append([]float64(nil), boundaries...), tags: cloned,
	})
}

func (s *deferredToolsMetricSinkForTest) RecordDuration(string, time.Duration, map[string]string) {}

func (s *deferredToolsMetricSinkForTest) snapshot() []deferredToolsHistogramSample {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]deferredToolsHistogramSample(nil), s.samples...)
}

// TestDeferredToolsFragmentMetricsLikeRust pins Rust #47873: snapshot and delta
// renders emit before/after namespace and byte histograms with the shared
// logarithmic boundaries.
func TestDeferredToolsFragmentMetricsLikeRust(t *testing.T) {
	sink := &deferredToolsMetricSinkForTest{}
	router := NewRuntimeRouter(RuntimeServices{SkillShadowMetrics: sink})
	defer router.Close()
	router.recordDeferredToolsFragmentMetrics(&contextfrag.DeferredToolsFragmentReport{
		Snapshot:         true,
		BeforeNamespaces: 5,
		BeforeBytes:      9000,
		AfterNamespaces:  3,
		AfterBytes:       4000,
	})
	samples := sink.snapshot()
	if len(samples) != 4 {
		t.Fatalf("samples = %d, want 4: %#v", len(samples), samples)
	}
	want := []struct {
		name  string
		value int
		stage string
	}{
		{telemetry.ThreadToolsNamespacesTotalMetric, 5, "before"},
		{telemetry.ThreadToolsFragmentBytesMetric, 9000, "before"},
		{telemetry.ThreadToolsNamespacesTotalMetric, 3, "after"},
		{telemetry.ThreadToolsFragmentBytesMetric, 4000, "after"},
	}
	for index, sample := range samples {
		if sample.name != want[index].name || sample.value != want[index].value {
			t.Fatalf("sample %d = %#v, want %s=%d", index, sample, want[index].name, want[index].value)
		}
		if sample.tags["stage"] != want[index].stage || sample.tags["kind"] != "snapshot" {
			t.Fatalf("sample %d tags = %#v", index, sample.tags)
		}
		if len(sample.boundaries) != len(telemetry.ThreadToolsMetricBoundaries) {
			t.Fatalf("sample %d boundaries = %d, want %d", index, len(sample.boundaries), len(telemetry.ThreadToolsMetricBoundaries))
		}
	}

	sink.samples = nil
	router.recordDeferredToolsFragmentMetrics(&contextfrag.DeferredToolsFragmentReport{BeforeNamespaces: 1, BeforeBytes: 10, AfterNamespaces: 1, AfterBytes: 10})
	samples = sink.snapshot()
	if len(samples) != 4 || samples[0].tags["kind"] != "delta" {
		t.Fatalf("delta samples = %#v", samples)
	}
}
