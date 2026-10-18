package windowssandbox

import (
	"errors"
	"testing"
	"time"

	"codex_go/metrics"
)

type worldWritableMetric struct {
	name  string
	value int
	tags  map[string]string
}

type worldWritableMetricRecorder struct {
	histograms []worldWritableMetric
}

func (r *worldWritableMetricRecorder) RecordDuration(string, time.Duration, map[string]string) {}

func (r *worldWritableMetricRecorder) Counter(string, int, map[string]string) {}

func (r *worldWritableMetricRecorder) Histogram(name string, value int, tags map[string]string) {
	r.histograms = append(r.histograms, worldWritableMetric{name: name, value: value, tags: tags})
}

func TestWorldWritableScanTelemetryLikeRust(t *testing.T) {
	previous := metrics.InstallGlobal(nil)
	t.Cleanup(func() { metrics.InstallGlobal(previous) })

	recorder := &worldWritableMetricRecorder{}
	metrics.InstallGlobal(recorder)

	recordWorldWritableScanResult(worldWritableAuditResult([]string{"one", "two"}, false), nil)
	recordWorldWritableScanResult(worldWritableAuditResult([]string{"one"}, true), errors.New("scan failed"))

	if len(recorder.histograms) != 2 {
		t.Fatalf("histogram count = %d, want 2", len(recorder.histograms))
	}
	success := recorder.histograms[0]
	if success.name != worldWritableScanFlaggedDirectoriesMetric || success.value != 2 || success.tags["result"] != "success" {
		t.Fatalf("success histogram = %#v", success)
	}
	failure := recorder.histograms[1]
	if failure.name != worldWritableScanFlaggedDirectoriesMetric || failure.value != 0 || failure.tags["result"] != "error" {
		t.Fatalf("failure histogram = %#v", failure)
	}
}
