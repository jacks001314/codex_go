package telemetry

import "time"

// Rust parity: codex-rs/otel/src/metrics/timer.rs. A timer captures the tags
// supplied at creation and records the elapsed milliseconds when it is stopped.
// Rust records from `Drop`; Go has no destructors, so callers either defer
// Stop (record with no additional tags) or call Record explicitly.
type Timer struct {
	name   string
	tags   map[string]string
	client *MetricsClient
	start  time.Time
}

// StartTimer starts a duration timer for the metric.
func (c *MetricsClient) StartTimer(name string, tags map[string]string) *Timer {
	return &Timer{name: name, tags: cloneMetricTagMap(tags), client: c, start: time.Now()}
}

// Record records the elapsed duration in milliseconds with the timer's tags
// plus the additional tags (Rust's `Timer::record`).
func (t *Timer) Record(additionalTags map[string]string) {
	if t == nil || t.client == nil {
		return
	}
	// Rust extends the tag vector with the additional tags first and the
	// timer's own tags last, and the client's map merge lets the later value
	// win, so a timer tag overrides an identical additional tag.
	combined := make(map[string]string, len(t.tags)+len(additionalTags))
	for key, value := range additionalTags {
		combined[key] = value
	}
	for key, value := range t.tags {
		combined[key] = value
	}
	t.client.RecordDuration(t.name, time.Since(t.start), combined)
}

// Stop records the elapsed duration with the timer's tags only, mirroring the
// record Rust performs when the timer is dropped.
func (t *Timer) Stop() {
	t.Record(nil)
}
