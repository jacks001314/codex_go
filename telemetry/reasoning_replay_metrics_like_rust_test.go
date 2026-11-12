package telemetry

import (
	"context"
	"sync"
	"testing"
	"time"

	"codex_go/metrics"
	"codex_go/model"
)

type reasoningReplaySample struct {
	name       string
	value      int
	tags       map[string]string
	boundaries []float64
}

type reasoningReplayRecorder struct {
	mu         sync.Mutex
	counters   []reasoningReplaySample
	histograms []reasoningReplaySample
}

func (r *reasoningReplayRecorder) RecordDuration(string, time.Duration, map[string]string) {}

func (r *reasoningReplayRecorder) Counter(name string, inc int, tags map[string]string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.counters = append(r.counters, reasoningReplaySample{name: name, value: inc, tags: cloneSampleTags(tags)})
}

func (r *reasoningReplayRecorder) Histogram(name string, value int, tags map[string]string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.histograms = append(r.histograms, reasoningReplaySample{name: name, value: value, tags: cloneSampleTags(tags)})
}

func (r *reasoningReplayRecorder) HistogramWithBounds(name string, value int, boundaries []float64, tags map[string]string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.histograms = append(r.histograms, reasoningReplaySample{
		name: name, value: value, tags: cloneSampleTags(tags), boundaries: append([]float64(nil), boundaries...),
	})
}

func cloneSampleTags(tags map[string]string) map[string]string {
	cloned := map[string]string{}
	for key, value := range tags {
		cloned[key] = value
	}
	return cloned
}

// Mirrors Rust #61017: every completed response classifies its reasoning-replay
// report, and only a report the response carried produces per-reason histograms.
func TestReasoningReplayMetricsLikeRust(t *testing.T) {
	recorder := &reasoningReplayRecorder{}
	metrics.InstallGlobal(recorder)
	t.Cleanup(func() { metrics.InstallGlobal(nil) })

	session := NewSessionTelemetry(SessionTelemetryMetadata{ConversationID: "thread-1", ProductSKU: "codex"})

	session.RecordSSEEventCompleted(context.Background(), model.SSECompletedRecord{UsageReported: true})
	if len(recorder.counters) != 1 || recorder.counters[0].name != ReasoningReplayCountMetric {
		t.Fatalf("counters = %#v, want one reasoning-replay counter", recorder.counters)
	}
	if recorder.counters[0].tags["report"] != "unavailable" || recorder.counters[0].tags["product_sku"] != "codex" {
		t.Fatalf("counter tags = %#v", recorder.counters[0].tags)
	}
	if len(recorder.histograms) != 0 {
		t.Fatalf("histograms = %#v, want none for an unavailable report", recorder.histograms)
	}

	empty := &model.ReasoningRemovals{Counts: map[model.ReasoningRemovalReason]int64{}}
	session.RecordSSEEventCompleted(context.Background(), model.SSECompletedRecord{ReasoningRemovals: empty})
	if recorder.counters[1].tags["report"] != "no_removals_reported" {
		t.Fatalf("counter tags = %#v", recorder.counters[1].tags)
	}
	if len(recorder.histograms) != len(model.AllReasoningRemovalReasons) {
		t.Fatalf("histograms = %#v, want one per bounded reason", recorder.histograms)
	}
	for _, sample := range recorder.histograms {
		if sample.name != ReasoningItemsRemovedMetric || sample.value != 0 {
			t.Fatalf("histogram = %#v, want a zero observation", sample)
		}
		if len(sample.boundaries) != len(ReasoningItemsRemovedBoundaries) ||
			sample.boundaries[0] != 0 || sample.boundaries[len(sample.boundaries)-1] != 256 {
			t.Fatalf("boundaries = %#v", sample.boundaries)
		}
	}

	recorder.histograms = nil
	reported := &model.ReasoningRemovals{Counts: map[model.ReasoningRemovalReason]int64{
		model.ReasoningRemovalPrefixMismatch: 2,
		model.ReasoningRemovalOther:          1,
	}}
	session.RecordSSEEventCompleted(context.Background(), model.SSECompletedRecord{ReasoningRemovals: reported})
	if recorder.counters[2].tags["report"] != "prefix_mismatch_reported" {
		t.Fatalf("counter tags = %#v", recorder.counters[2].tags)
	}
	values := map[string]int{}
	for _, sample := range recorder.histograms {
		values[sample.tags["reason"]] = sample.value
		if sample.tags["product_sku"] != "codex" {
			t.Fatalf("histogram tags = %#v, want the product SKU", sample.tags)
		}
	}
	if values["prefix_mismatch"] != 2 || values["other"] != 1 || values["turn_boundary"] != 0 {
		t.Fatalf("histogram values = %#v", values)
	}
}
