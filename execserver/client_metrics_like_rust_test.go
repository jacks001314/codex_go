package execserver

import (
	"context"
	"testing"
	"time"

	"codex_go/metrics"
)

type execServerMetricRecorder struct {
	counters []execServerMetricCounter
}

type execServerMetricCounter struct {
	name string
	tags map[string]string
}

func (r *execServerMetricRecorder) Counter(name string, _ int, tags map[string]string) {
	r.counters = append(r.counters, execServerMetricCounter{name: name, tags: tags})
}

func (r *execServerMetricRecorder) Histogram(string, int, map[string]string) {}

func (r *execServerMetricRecorder) RecordDuration(string, time.Duration, map[string]string) {}

// Mirrors Rust #42883: every client RPC attempt is counted before local
// admission, while notifications do not enter the call path.
func TestExecServerClientRequestMetricCountsAttemptsLikeRust(t *testing.T) {
	recorder := &execServerMetricRecorder{}
	metrics.InstallGlobal(recorder)
	t.Cleanup(func() { metrics.InstallGlobal(nil) })

	client := &Client{closed: true}
	if err := client.call(context.Background(), "fs/readFile", map[string]any{}, nil); err == nil {
		t.Fatal("closed client call unexpectedly succeeded")
	}
	if len(recorder.counters) != 1 {
		t.Fatalf("counters = %#v, want one RPC attempt", recorder.counters)
	}
	if recorder.counters[0].name != metrics.ExecServerClientRequestCountMetric ||
		recorder.counters[0].tags["method"] != "fs/readFile" {
		t.Fatalf("counter = %#v", recorder.counters[0])
	}
	if err := client.notify(context.Background(), "fs/changed", nil); err == nil {
		t.Fatal("closed client notification unexpectedly succeeded")
	}
	if len(recorder.counters) != 1 {
		t.Fatalf("notification incremented the RPC counter: %#v", recorder.counters)
	}
}
