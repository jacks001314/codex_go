package appserver

import (
	"sync"
	"testing"
	"time"
)

type taggedVoiceMetric struct {
	name string
	tags map[string]string
}

type taggedVoiceMetrics struct {
	mu        sync.Mutex
	counters  []taggedVoiceMetric
	durations []taggedVoiceMetric
}

func (m *taggedVoiceMetrics) Counter(name string, _ int, tags map[string]string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	cloned := make(map[string]string, len(tags))
	for key, value := range tags {
		cloned[key] = value
	}
	m.counters = append(m.counters, taggedVoiceMetric{name: name, tags: cloned})
}

func (m *taggedVoiceMetrics) RecordDuration(name string, _ time.Duration, tags map[string]string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	cloned := make(map[string]string, len(tags))
	for key, value := range tags {
		cloned[key] = value
	}
	m.durations = append(m.durations, taggedVoiceMetric{name: name, tags: cloned})
}

// Rust #52756: a voice session records at most one failure with bounded cause
// and lifecycle phase, and the duration carries the terminal outcome.
func TestVoiceSessionFailureMetricsLikeRust(t *testing.T) {
	metrics := &taggedVoiceMetrics{}
	router := NewRuntimeRouter(RuntimeServices{VoiceMetrics: metrics})
	t.Cleanup(func() { _ = router.Close() })

	router.recordVoiceSessionFailure("thread-1", "backend", "starting")
	router.recordVoiceSessionFailure("thread-1", "transport", "active")
	router.recordVoiceSessionFailure("thread-2", "not-a-cause", "not-a-phase")
	router.recordVoiceSessionFailure("thread-3", "not-a-cause", "starting")
	if len(metrics.counters) != 2 {
		t.Fatalf("failure counters = %#v, want two records", metrics.counters)
	}
	if got := metrics.counters[0]; got.name != voiceSessionFailureMetric ||
		got.tags["cause"] != "backend" || got.tags["phase"] != "starting" {
		t.Fatalf("failure metric = %#v", got)
	}
	if got := metrics.counters[1]; got.tags["cause"] != "connection_unknown" || got.tags["phase"] != "starting" {
		t.Fatalf("unknown cause was not bounded: %#v", got)
	}
	if !router.voiceSessionFailed("thread-1") {
		t.Fatal("the session did not retain its failure state")
	}
	if tags := router.voiceDurationTags("thread-1"); tags["outcome"] != "failure" {
		t.Fatalf("duration tags = %#v, want failure", tags)
	}
	if tags := router.voiceDurationTags("thread-2"); tags["outcome"] != "success" {
		t.Fatalf("duration tags = %#v, want success", tags)
	}
	router.clearVoiceSessionFailure("thread-1")
	if router.voiceSessionFailed("thread-1") {
		t.Fatal("the failure state survived a new session")
	}
}

// Duration metrics are deduplicated per session start, not per thread.
func TestVoiceDurationDeduplicatesPerSessionLikeRust(t *testing.T) {
	router := NewRuntimeRouter(RuntimeServices{})
	t.Cleanup(func() { _ = router.Close() })
	first := time.Unix(1_700_000_000, 0).UTC()
	if !router.claimVoiceDuration("thread-1", first) {
		t.Fatal("the first session was rejected")
	}
	if router.claimVoiceDuration("thread-1", first) {
		t.Fatal("a repeated closed notification recorded the duration twice")
	}
	if !router.claimVoiceDuration("thread-1", first.Add(time.Minute)) {
		t.Fatal("a new session on the same thread was rejected")
	}
}
