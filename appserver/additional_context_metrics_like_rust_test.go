package appserver

import (
	"strings"
	"sync"
	"testing"
	"time"

	"codex_go/metrics"
	"codex_go/telemetry"
	"codex_go/turn"
)

type additionalContextMetricSample struct {
	name       string
	value      int
	tags       map[string]string
	boundaries []float64
}

type additionalContextMetricRecorder struct {
	mu      sync.Mutex
	samples []additionalContextMetricSample
}

func (r *additionalContextMetricRecorder) RecordDuration(string, time.Duration, map[string]string) {}

func (r *additionalContextMetricRecorder) Counter(name string, inc int, tags map[string]string) {
	r.record(name, inc, tags, nil)
}

func (r *additionalContextMetricRecorder) Histogram(name string, value int, tags map[string]string) {
	r.record(name, value, tags, nil)
}

func (r *additionalContextMetricRecorder) HistogramWithBounds(name string, value int, boundaries []float64, tags map[string]string) {
	r.record(name, value, tags, boundaries)
}

func (r *additionalContextMetricRecorder) record(name string, value int, tags map[string]string, boundaries []float64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	cloned := make(map[string]string, len(tags))
	for key, tag := range tags {
		cloned[key] = tag
	}
	r.samples = append(r.samples, additionalContextMetricSample{name: name, value: value, tags: cloned, boundaries: boundaries})
}

func (r *additionalContextMetricRecorder) take() []additionalContextMetricSample {
	r.mu.Lock()
	defer r.mu.Unlock()
	samples := r.samples
	r.samples = nil
	return samples
}

// Mirrors Rust #58144: additional-context counts and before/after byte sizes are
// recorded per submitted entry with kind, truncation, and explicit-name tags.
func TestAdditionalContextMetricsLikeRust(t *testing.T) {
	recorder := &additionalContextMetricRecorder{}
	metrics.InstallGlobal(nil)
	metrics.InstallGlobal(recorder)
	defer metrics.InstallGlobal(nil)

	router := &RuntimeRouter{}
	name := "browser"
	largeName := "large"
	router.mergeAdditionalContext("thread-a", map[string]turn.AdditionalContextEntry{
		"application_limit": {Kind: turn.AdditionalContextApplication, Value: strings.Repeat("a", 4000)},
		"application_over":  {Kind: turn.AdditionalContextApplication, Value: strings.Repeat("a", 4001)},
		"application_large": {Kind: turn.AdditionalContextApplication, Name: &largeName, Value: strings.Repeat("a", 40000)},
		"opaque_named":      {Kind: turn.AdditionalContextUntrusted, Name: &name, Value: strings.Repeat("b", 4001)},
		"opaque_unnamed":    {Kind: turn.AdditionalContextUntrusted, Value: strings.Repeat("\u00e9", 2001)},
		"opaque_empty":      {Kind: turn.AdditionalContextUntrusted, Value: ""},
	}, false)

	samples := recorder.take()
	counts := map[string]map[string]string{}
	sizes := map[string]map[string]additionalContextMetricSample{}
	for _, sample := range samples {
		switch sample.name {
		case telemetry.AdditionalContextCountMetric:
			if sample.value != 1 {
				t.Fatalf("count increment = %d, want 1", sample.value)
			}
			counts[sample.tags["kind"]+"/"+sample.tags["truncated"]+"/"+sample.tags["name"]] = sample.tags
		case telemetry.AdditionalContextValueBytesMetric:
			if len(sample.boundaries) != len(telemetry.AdditionalContextValueBytesBuckets) {
				t.Fatalf("size histogram boundaries = %d, want %d", len(sample.boundaries), len(telemetry.AdditionalContextValueBytesBuckets))
			}
			key := sample.tags["kind"] + "/" + sample.tags["truncated"] + "/" + sample.tags["name"]
			if sizes[key] == nil {
				sizes[key] = map[string]additionalContextMetricSample{}
			}
			sizes[key][sample.tags["stage"]] = sample
		default:
			t.Fatalf("unexpected metric %q", sample.name)
		}
	}
	for _, want := range []string{"application/false/", "application/true/", "untrusted/true/browser", "untrusted/true/", "untrusted/false/"} {
		if _, ok := counts[want]; !ok {
			t.Fatalf("missing count sample %q in %#v", want, counts)
		}
	}
	if _, ok := counts["untrusted/true/opaque_named"]; ok {
		t.Fatal("opaque source key must never become a name tag")
	}
	limit := sizes["application/false/"]
	if limit["before"].value != 4000 || limit["after"].value != 4000 {
		t.Fatalf("unchanged entry sizes = %#v", limit)
	}
	// A value just past the budget still reports its original size; `after`
	// includes the truncation marker, which can exceed the removed bytes
	// (Rust #58144).
	over := sizes["application/true/"]
	if over["before"].value != 4001 || over["after"].value == 0 {
		t.Fatalf("slightly over-budget entry counts = %#v", over)
	}
	large := sizes["application/true/large"]
	if large["before"].value != 40000 || large["after"].value >= large["before"].value {
		t.Fatalf("large entry sizes = %#v", large)
	}
	multibyte := sizes["untrusted/true/"]
	if multibyte["before"].value != 4002 || multibyte["after"].value == multibyte["before"].value {
		t.Fatalf("multibyte entry sizes = %#v", multibyte)
	}
	empty := sizes["untrusted/false/"]
	if empty["before"].value != 0 || empty["after"].value != 0 {
		t.Fatalf("empty entry sizes = %#v", empty)
	}
}
