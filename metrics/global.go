// Package metrics holds the process-global metrics recorder. It is a leaf
// package so library code that must not import codex_go/telemetry (the model
// package reaches telemetry only through the memories cycle) can still record
// global metrics (Rust `codex_otel::start_global_timer`).
package metrics

import (
	"sync/atomic"
	"time"
)

// Recorder records metrics on the process-global recorder.
// telemetry.MetricsClient satisfies it.
type Recorder interface {
	RecordDuration(name string, duration time.Duration, tags map[string]string)
	Counter(name string, inc int, tags map[string]string)
	Histogram(name string, value int, tags map[string]string)
}

var global atomic.Value // stores Recorder

// InstallGlobal installs the process-global recorder. A nil recorder clears it.
func InstallGlobal(recorder Recorder) Recorder {
	if recorder == nil {
		global.Store(recorderBox{})
		return nil
	}
	global.Store(recorderBox{recorder: recorder})
	return recorder
}

// Global returns the installed process-global recorder, if any.
func Global() Recorder {
	box, ok := global.Load().(recorderBox)
	if !ok {
		return nil
	}
	return box.recorder
}

// Counter records a counter increment on the process-global recorder
// (Rust `codex_otel::global()` counters). It is nil-safe.
func Counter(name string, inc int, tags map[string]string) {
	if recorder := Global(); recorder != nil {
		recorder.Counter(name, inc, cloneTags(tags))
	}
}

// Histogram records a histogram observation on the process-global recorder. It
// is nil-safe.
func Histogram(name string, value int, tags map[string]string) {
	if recorder := Global(); recorder != nil {
		recorder.Histogram(name, value, cloneTags(tags))
	}
}

// RecordDuration records a duration on the process-global recorder. It is
// nil-safe.
func RecordDuration(name string, duration time.Duration, tags map[string]string) {
	if recorder := Global(); recorder != nil {
		recorder.RecordDuration(name, duration, cloneTags(tags))
	}
}

func cloneTags(tags map[string]string) map[string]string {
	if len(tags) == 0 {
		return nil
	}
	cloned := make(map[string]string, len(tags))
	for key, value := range tags {
		cloned[key] = value
	}
	return cloned
}

type recorderBox struct {
	recorder Recorder
}

// Timer records its elapsed duration under the captured tags when stopped.
type Timer struct {
	recorder Recorder
	name     string
	tags     map[string]string
	start    time.Time
}

// StartTimer starts a timer on the process-global recorder. It returns nil when
// no recorder is installed; Timer.Stop is nil-safe, so callers may always defer
// Stop.
func StartTimer(name string, tags map[string]string) *Timer {
	recorder := Global()
	if recorder == nil {
		return nil
	}
	cloned := make(map[string]string, len(tags))
	for key, value := range tags {
		cloned[key] = value
	}
	return &Timer{recorder: recorder, name: name, tags: cloned, start: time.Now()}
}

// Stop records the elapsed duration in milliseconds.
func (t *Timer) Stop() {
	if t == nil || t.recorder == nil {
		return
	}
	t.recorder.RecordDuration(t.name, time.Since(t.start), t.tags)
}
