package rollout

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReduceTraceBundleWritesState(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), []byte(`{"trace_id":"trace-1","root_thread_id":"thread-1"}`), 0o600); err != nil {
		t.Fatalf("WriteFile manifest error = %v", err)
	}
	trace := `{"seq":1,"type":"thread_started","thread_id":"thread-1","payload_id":"payload-1"}` + "\n" +
		`{"seq":2,"type":"turn_completed","thread_id":"thread-1","turn_id":"turn-1"}` + "\n"
	if err := os.WriteFile(filepath.Join(dir, "trace.jsonl"), []byte(trace), 0o600); err != nil {
		t.Fatalf("WriteFile trace error = %v", err)
	}
	output, reduced, err := ReduceTraceBundle(&TraceReduceOptions{BundleDir: dir})
	if err != nil {
		t.Fatalf("ReduceTraceBundle() error = %v", err)
	}
	if output != filepath.Join(dir, ReducedStateFileName) {
		t.Fatalf("output = %q", output)
	}
	if reduced.EventCount != 2 || len(reduced.Threads) != 1 || reduced.Threads[0] != "thread-1" {
		t.Fatalf("reduced = %#v", reduced)
	}
	data, err := os.ReadFile(output)
	if err != nil {
		t.Fatalf("ReadFile state error = %v", err)
	}
	if !strings.Contains(string(data), `"event_count": 2`) || !strings.Contains(string(data), `"thread-1"`) {
		t.Fatalf("state = %q", string(data))
	}
}
