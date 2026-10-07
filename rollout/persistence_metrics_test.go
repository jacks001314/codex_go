package rollout

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"codex_go/metrics"
)

// Mirrors the Rust persistence-metrics vector for #50454: a paginated
// ItemCompleted CommandExecution whose aggregated output is twice the persisted
// cap (codex-rs rollout/src/persistence_metrics_tests.rs
// `projected_items_report_post_projection_bytes`).
func rolloutPersistenceProbeCommandItem(output string) json.RawMessage {
	item := map[string]any{
		"type":              "CommandExecution",
		"id":                "exec",
		"command":           []string{"echo"},
		"cwd":               "/tmp",
		"source":            "agent",
		"status":            "completed",
		"aggregated_output": output,
		"exit_code":         0,
	}
	raw, err := json.Marshal(item)
	if err != nil {
		panic(err)
	}
	return raw
}

type rolloutPersistenceMetricSample struct {
	name       string
	value      int
	boundaries []float64
	tags       map[string]string
}

type rolloutPersistenceMetricsRecorder struct {
	mu         sync.Mutex
	histograms []rolloutPersistenceMetricSample
	counters   []rolloutPersistenceMetricSample
}

func (r *rolloutPersistenceMetricsRecorder) RecordDuration(string, time.Duration, map[string]string) {
}

func (r *rolloutPersistenceMetricsRecorder) Counter(name string, inc int, tags map[string]string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.counters = append(r.counters, rolloutPersistenceMetricSample{name: name, value: inc, tags: cloneRolloutPersistenceTestTags(tags)})
}

func (r *rolloutPersistenceMetricsRecorder) Histogram(name string, value int, tags map[string]string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.histograms = append(r.histograms, rolloutPersistenceMetricSample{name: name, value: value, tags: cloneRolloutPersistenceTestTags(tags)})
}

func (r *rolloutPersistenceMetricsRecorder) HistogramWithBounds(name string, value int, boundaries []float64, tags map[string]string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.histograms = append(r.histograms, rolloutPersistenceMetricSample{
		name:       name,
		value:      value,
		boundaries: append([]float64(nil), boundaries...),
		tags:       cloneRolloutPersistenceTestTags(tags),
	})
}

func (r *rolloutPersistenceMetricsRecorder) samples(name string) []rolloutPersistenceMetricSample {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := []rolloutPersistenceMetricSample{}
	for _, sample := range r.histograms {
		if sample.name == name {
			out = append(out, sample)
		}
	}
	return out
}

func (r *rolloutPersistenceMetricsRecorder) counterSamples() []rolloutPersistenceMetricSample {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]rolloutPersistenceMetricSample(nil), r.counters...)
}

func cloneRolloutPersistenceTestTags(tags map[string]string) map[string]string {
	cloned := make(map[string]string, len(tags))
	for key, value := range tags {
		cloned[key] = value
	}
	return cloned
}

func installRolloutPersistenceMetricsRecorder(t *testing.T) *rolloutPersistenceMetricsRecorder {
	t.Helper()
	recorder := &rolloutPersistenceMetricsRecorder{}
	metrics.InstallGlobal(recorder)
	t.Cleanup(func() { metrics.InstallGlobal(nil) })
	return recorder
}

func findRolloutPersistenceSample(samples []rolloutPersistenceMetricSample, want map[string]string) (rolloutPersistenceMetricSample, bool) {
	for _, sample := range samples {
		matches := true
		for key, value := range want {
			if sample.tags[key] != value {
				matches = false
				break
			}
		}
		if matches {
			return sample, true
		}
	}
	return rolloutPersistenceMetricSample{}, false
}

// selectedRolloutThreadID mirrors Rust's `thread_sampling_is_stable_and_selects_whole_threads`:
// thread ids are sampled by whole threads, so the same id always lands on the
// same side of the 1% bucket.
func selectedRolloutThreadID(t *testing.T, sampled bool) string {
	t.Helper()
	for value := 0; value < 10_000; value++ {
		id := fmt.Sprintf("00000000-0000-0000-0000-%012x", value)
		if rolloutPersistenceThreadSampled(id) == sampled {
			if rolloutPersistenceThreadSampled(id) != sampled {
				t.Fatalf("thread sampling is not stable for %s", id)
			}
			return id
		}
	}
	t.Fatalf("no thread id with sampled=%v", sampled)
	return ""
}

// #50454: a persisted item reports its size before and after the persistence
// projection plus the positive reduction.
func TestRolloutPersistenceMetricsMeasureSizeReductionLikeRust(t *testing.T) {
	threadID := selectedRolloutThreadID(t, true)
	recorder := installRolloutPersistenceMetricsRecorder(t)

	rolloutRecorder, err := NewRecorder(&CreateParams{
		CodexHome:   t.TempDir(),
		ThreadID:    threadID,
		HistoryMode: "paginated",
	})
	if err != nil {
		t.Fatalf("NewRecorder() error = %v", err)
	}
	defer rolloutRecorder.Close()

	item := rolloutPersistenceProbeCommandItem(strings.Repeat("x", persistedCommandOutputMaxBytes*2))
	persisted := applyPersistedItemTruncation(item)
	if len(persisted) >= len(item) {
		t.Fatalf("projection did not reduce the item: %d -> %d", len(item), len(persisted))
	}
	if err := rolloutRecorder.AppendItemCompleted(item, "turn", time.Now(), time.Now()); err != nil {
		t.Fatalf("AppendItemCompleted() error = %v", err)
	}

	wantTags := map[string]string{
		"decision":          "kept",
		"rollout_item_type": "event.item_completed.command_execution",
		"encoding":          "rollout_item_json_v1",
		"sample_rate":       "0.01",
	}
	before, ok := findRolloutPersistenceSample(recorder.samples(rolloutPersistenceItemBytesMetric), map[string]string{"stage": "before_projection"})
	if !ok {
		t.Fatalf("item_bytes_v2 before_projection samples = %#v", recorder.samples(rolloutPersistenceItemBytesMetric))
	}
	if before.value != len(item) {
		t.Fatalf("before_projection value = %d, want %d", before.value, len(item))
	}
	after, ok := findRolloutPersistenceSample(recorder.samples(rolloutPersistenceItemBytesMetric), map[string]string{"stage": "after_projection"})
	if !ok {
		t.Fatalf("item_bytes_v2 after_projection samples = %#v", recorder.samples(rolloutPersistenceItemBytesMetric))
	}
	if after.value != len(persisted) {
		t.Fatalf("after_projection value = %d, want %d", after.value, len(persisted))
	}
	for key, value := range wantTags {
		if before.tags[key] != value || after.tags[key] != value {
			t.Fatalf("item_bytes_v2 tags = %#v / %#v, want %s=%s", before.tags, after.tags, key, value)
		}
	}
	if !equalFloatSlices(before.boundaries, rolloutPersistenceBytesBoundaries) || !equalFloatSlices(after.boundaries, rolloutPersistenceBytesBoundaries) {
		t.Fatalf("item_bytes_v2 boundaries = %#v / %#v", before.boundaries, after.boundaries)
	}
	removed, ok := findRolloutPersistenceSample(recorder.samples(rolloutPersistenceBytesRemovedMetric), nil)
	if !ok {
		t.Fatalf("bytes_removed samples = %#v", recorder.samples(rolloutPersistenceBytesRemovedMetric))
	}
	if removed.value != len(item)-len(persisted) {
		t.Fatalf("bytes_removed value = %d, want %d", removed.value, len(item)-len(persisted))
	}
	if removed.value <= 0 {
		t.Fatalf("bytes_removed value = %d, want a positive reduction", removed.value)
	}
	if !equalFloatSlices(removed.boundaries, rolloutPersistenceBytesBoundaries) {
		t.Fatalf("bytes_removed boundaries = %#v", removed.boundaries)
	}
	if len(recorder.counterSamples()) != 0 {
		t.Fatalf("unexpected counters = %#v", recorder.counterSamples())
	}
}

// A captured item that the projection leaves untouched keeps identical
// before/after sizes and reports no reduction.
func TestRolloutPersistenceMetricsSkipUnreducedItemsLikeRust(t *testing.T) {
	threadID := selectedRolloutThreadID(t, true)
	recorder := installRolloutPersistenceMetricsRecorder(t)

	rolloutRecorder, err := NewRecorder(&CreateParams{
		CodexHome:   t.TempDir(),
		ThreadID:    threadID,
		HistoryMode: "paginated",
	})
	if err != nil {
		t.Fatalf("NewRecorder() error = %v", err)
	}
	defer rolloutRecorder.Close()

	item := rolloutPersistenceProbeCommandItem("hello")
	if err := rolloutRecorder.AppendItemCompleted(item, "turn", time.Now(), time.Now()); err != nil {
		t.Fatalf("AppendItemCompleted() error = %v", err)
	}

	before, ok := findRolloutPersistenceSample(recorder.samples(rolloutPersistenceItemBytesMetric), map[string]string{"stage": "before_projection"})
	if !ok {
		t.Fatalf("item_bytes_v2 samples = %#v", recorder.samples(rolloutPersistenceItemBytesMetric))
	}
	after, ok := findRolloutPersistenceSample(recorder.samples(rolloutPersistenceItemBytesMetric), map[string]string{"stage": "after_projection"})
	if !ok {
		t.Fatalf("item_bytes_v2 samples = %#v", recorder.samples(rolloutPersistenceItemBytesMetric))
	}
	if before.value != after.value || before.value != len(item) {
		t.Fatalf("before/after = %d/%d, want both %d", before.value, after.value, len(item))
	}
	if samples := recorder.samples(rolloutPersistenceBytesRemovedMetric); len(samples) != 0 {
		t.Fatalf("bytes_removed samples = %#v, want none", samples)
	}
}

// Threads outside the 1% sample report nothing even when the item is reduced.
func TestRolloutPersistenceMetricsSampledByWholeThreadLikeRust(t *testing.T) {
	threadID := selectedRolloutThreadID(t, false)
	recorder := installRolloutPersistenceMetricsRecorder(t)

	rolloutRecorder, err := NewRecorder(&CreateParams{
		CodexHome:   t.TempDir(),
		ThreadID:    threadID,
		HistoryMode: "paginated",
	})
	if err != nil {
		t.Fatalf("NewRecorder() error = %v", err)
	}
	defer rolloutRecorder.Close()

	item := rolloutPersistenceProbeCommandItem(strings.Repeat("x", persistedCommandOutputMaxBytes*2))
	if err := rolloutRecorder.AppendItemCompleted(item, "turn", time.Now(), time.Now()); err != nil {
		t.Fatalf("AppendItemCompleted() error = %v", err)
	}
	if samples := recorder.samples(rolloutPersistenceItemBytesMetric); len(samples) != 0 {
		t.Fatalf("item_bytes_v2 samples = %#v, want none for an unsampled thread", samples)
	}
	if samples := recorder.samples(rolloutPersistenceBytesRemovedMetric); len(samples) != 0 {
		t.Fatalf("bytes_removed samples = %#v, want none for an unsampled thread", samples)
	}
}

// With no recorder installed the measurement path stays silent (the metrics
// package helpers are nil-safe).
func TestRolloutPersistenceMetricsWithoutRecorderIsSilent(t *testing.T) {
	threadID := selectedRolloutThreadID(t, true)
	metrics.InstallGlobal(nil)
	t.Cleanup(func() { metrics.InstallGlobal(nil) })

	rolloutRecorder, err := NewRecorder(&CreateParams{
		CodexHome:   t.TempDir(),
		ThreadID:    threadID,
		HistoryMode: "paginated",
	})
	if err != nil {
		t.Fatalf("NewRecorder() error = %v", err)
	}
	defer rolloutRecorder.Close()

	item := rolloutPersistenceProbeCommandItem(strings.Repeat("x", persistedCommandOutputMaxBytes*2))
	if err := rolloutRecorder.AppendItemCompleted(item, "turn", time.Now(), time.Now()); err != nil {
		t.Fatalf("AppendItemCompleted() error = %v", err)
	}
}

func equalFloatSlices(left []float64, right []float64) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
