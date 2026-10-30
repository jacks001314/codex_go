package execserver

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"codex_go/metrics"
)

type recordedExecServerCounter struct {
	name string
	inc  int
	tags map[string]string
}

type recordedExecServerDuration struct {
	name     string
	duration time.Duration
	tags     map[string]string
}

// execServerMetricsCapture implements the process recorder plus the described
// counter and seconds-duration instruments the request metrics use.
type execServerMetricsCapture struct {
	mu        sync.Mutex
	counters  []recordedExecServerCounter
	durations []recordedExecServerDuration
}

func (c *execServerMetricsCapture) Counter(name string, inc int, tags map[string]string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.counters = append(c.counters, recordedExecServerCounter{name: name, inc: inc, tags: cloneTagsForTest(tags)})
}

func (c *execServerMetricsCapture) CounterWithDescription(name string, _ string, inc int, tags map[string]string) {
	c.Counter(name, inc, tags)
}

func (c *execServerMetricsCapture) Histogram(string, int, map[string]string) {}

func (c *execServerMetricsCapture) RecordDuration(name string, duration time.Duration, tags map[string]string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.durations = append(c.durations, recordedExecServerDuration{name: name, duration: duration, tags: cloneTagsForTest(tags)})
}

func (c *execServerMetricsCapture) RecordDurationSecondsWithDescription(name string, _ string, duration time.Duration, tags map[string]string) {
	c.RecordDuration(name, duration, tags)
}

func cloneTagsForTest(tags map[string]string) map[string]string {
	cloned := make(map[string]string, len(tags))
	for key, value := range tags {
		cloned[key] = value
	}
	return cloned
}

func (c *execServerMetricsCapture) requests() []recordedExecServerCounter {
	c.mu.Lock()
	defer c.mu.Unlock()
	requests := make([]recordedExecServerCounter, 0, len(c.counters))
	for _, counter := range c.counters {
		if counter.name == execServerRequestsTotalMetric {
			requests = append(requests, counter)
		}
	}
	return requests
}

func (c *execServerMetricsCapture) durationCount(name string, tags map[string]string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	count := 0
	for _, duration := range c.durations {
		if duration.name != name {
			continue
		}
		matches := true
		for key, value := range tags {
			if duration.tags[key] != value {
				matches = false
				break
			}
		}
		if matches {
			count++
		}
	}
	return count
}

func installExecServerMetricsCapture(t *testing.T) *execServerMetricsCapture {
	t.Helper()
	capture := &execServerMetricsCapture{}
	previous := metrics.InstallGlobal(capture)
	t.Cleanup(func() { metrics.InstallGlobal(previous) })
	return capture
}

func serveExecServerLine(t *testing.T, line string, stdout *bytes.Buffer) error {
	t.Helper()
	server := NewServer()
	return server.Serve(context.Background(), strings.NewReader(line+"\n"), stdout)
}

// TestExecServerRequestMetricsRecordDispatchAndTotalLikeRust covers Rust
// #44207: every completed request records one counter and the dispatch, total,
// and queue durations under the same bounded method/result tags.
func TestExecServerRequestMetricsRecordDispatchAndTotalLikeRust(t *testing.T) {
	capture := installExecServerMetricsCapture(t)
	var stdout bytes.Buffer
	if err := serveExecServerLine(t, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`, &stdout); err != nil {
		t.Fatalf("serve initialize error = %v", err)
	}
	requests := capture.requests()
	if len(requests) != 1 {
		t.Fatalf("request counters = %#v", requests)
	}
	if requests[0].tags["method"] != MethodInitialize || requests[0].tags["result"] != "success" || requests[0].inc != 1 {
		t.Fatalf("initialize counter = %#v", requests[0])
	}
	tags := map[string]string{"method": MethodInitialize, "result": "success"}
	for _, name := range []string{
		execServerRequestDurationMetric,
		execServerRequestTotalDurationMetric,
		execServerRequestQueueDurationMetric,
	} {
		if got := capture.durationCount(name, tags); got != 1 {
			t.Fatalf("%s observations = %d, want 1", name, got)
		}
	}
	capture.mu.Lock()
	total := time.Duration(0)
	dispatch := time.Duration(0)
	for _, duration := range capture.durations {
		switch duration.name {
		case execServerRequestTotalDurationMetric:
			total = duration.duration
		case execServerRequestDurationMetric:
			dispatch = duration.duration
		}
	}
	capture.mu.Unlock()
	if total < dispatch {
		t.Fatalf("total duration %v is shorter than dispatch duration %v", total, dispatch)
	}
}

// TestExecServerRequestMetricsLabelUnknownMethodsLikeRust keeps unrouted
// methods out of the tag cardinality budget and marks them as errors.
func TestExecServerRequestMetricsLabelUnknownMethodsLikeRust(t *testing.T) {
	capture := installExecServerMetricsCapture(t)
	var stdout bytes.Buffer
	if err := serveExecServerLine(t, `{"jsonrpc":"2.0","id":2,"method":"stub/missing"}`, &stdout); err != nil {
		t.Fatalf("serve unknown method error = %v", err)
	}
	requests := capture.requests()
	if len(requests) != 1 || requests[0].tags["method"] != "unknown" || requests[0].tags["result"] != "error" {
		t.Fatalf("unknown method counters = %#v", requests)
	}
	if got := capture.durationCount(execServerRequestQueueDurationMetric, map[string]string{"method": "unknown"}); got != 1 {
		t.Fatalf("unknown queue observations = %d, want 1", got)
	}
}

type failingExecServerWriter struct{}

func (failingExecServerWriter) Write([]byte) (int, error) {
	return 0, errors.New("connection closed")
}

// TestExecServerRequestMetricsReportDisconnectedLikeRust records one
// disconnected completion when the response cannot be delivered.
func TestExecServerRequestMetricsReportDisconnectedLikeRust(t *testing.T) {
	capture := installExecServerMetricsCapture(t)
	server := NewServer()
	err := server.Serve(context.Background(), strings.NewReader(`{"jsonrpc":"2.0","id":3,"method":"initialize","params":{}}`+"\n"), failingExecServerWriter{})
	if err == nil {
		t.Fatal("Serve should report the write failure")
	}
	requests := capture.requests()
	if len(requests) != 1 || requests[0].tags["result"] != "disconnected" || requests[0].tags["method"] != MethodInitialize {
		t.Fatalf("disconnected counters = %#v", requests)
	}
	if got := capture.durationCount(execServerRequestTotalDurationMetric, map[string]string{"result": "disconnected"}); got != 1 {
		t.Fatalf("disconnected total observations = %d, want 1", got)
	}
}
