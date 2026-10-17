package sandbox

import (
	"runtime"
	"sync"
	"testing"
	"time"

	"codex_go/metrics"
)

type mxcAvailabilityRecorder struct {
	mu       sync.Mutex
	counters []mxcAvailabilityCounter
}

type mxcAvailabilityCounter struct {
	name string
	inc  int
	tags map[string]string
}

func (r *mxcAvailabilityRecorder) RecordDuration(string, time.Duration, map[string]string) {}
func (r *mxcAvailabilityRecorder) Histogram(string, int, map[string]string)                {}

func (r *mxcAvailabilityRecorder) Counter(name string, inc int, tags map[string]string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.counters = append(r.counters, mxcAvailabilityCounter{name: name, inc: inc, tags: tags})
}

func TestWindowsMXCAvailabilityRecordedOnceLikeRust(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("MXC availability telemetry is Windows-only")
	}
	previous := metrics.Global()
	defer metrics.InstallGlobal(previous)

	windowsMXCAvailabilityRecorded.Store(false)
	metrics.InstallGlobal(nil)
	recordMXCAvailabilityOnce()

	recorder := &mxcAvailabilityRecorder{}
	metrics.InstallGlobal(recorder)
	recordMXCAvailabilityOnce()
	recordMXCAvailabilityOnce()

	if len(recorder.counters) != 1 {
		t.Fatalf("counters = %#v, want one", recorder.counters)
	}
	counter := recorder.counters[0]
	if counter.name != WindowsMXCAvailableMetric || counter.inc != 1 || counter.tags["available"] != "false" {
		t.Fatalf("counter = %#v", counter)
	}
}
