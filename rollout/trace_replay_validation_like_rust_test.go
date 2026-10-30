package rollout

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestReplayTraceBundleRequiresTerminatedCompletionMarkerLikeRust covers Rust
// #58172: only a completed marker whose line was fully written (newline
// included) proves a clean recording.
func TestReplayTraceBundleRequiresTerminatedCompletionMarkerLikeRust(t *testing.T) {
	events := `{"seq":1,"type":"rollout_started","payload":{"type":"rollout_started","trace_id":"trace-1","root_thread_id":"thread-1"}}` + "\n" +
		`{"seq":2,"type":"thread_started","thread_id":"thread-1"}` + "\n"
	marker := `{"seq":3,"type":"recording_completed","payload":{"type":"recording_completed"}}`

	cases := []struct {
		name      string
		trace     string
		complete  bool
		wantCount int
	}{
		{name: "complete", trace: events + marker + "\n", complete: true, wantCount: 3},
		{name: "missing final newline", trace: events + marker, complete: false, wantCount: 2},
		{name: "truncated marker", trace: events + marker[:20], complete: false, wantCount: 2},
		{name: "no marker", trace: events, complete: false, wantCount: 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "manifest.json"), []byte(`{"trace_id":"trace-1"}`), 0o600); err != nil {
				t.Fatalf("WriteFile manifest error = %v", err)
			}
			if err := os.WriteFile(filepath.Join(dir, "trace.jsonl"), []byte(tc.trace), 0o600); err != nil {
				t.Fatalf("WriteFile trace error = %v", err)
			}
			reduced, err := ReplayTraceBundle(dir)
			if err != nil {
				t.Fatalf("ReplayTraceBundle() error = %v", err)
			}
			if reduced.RecordingComplete != tc.complete {
				t.Fatalf("RecordingComplete = %v, want %v", reduced.RecordingComplete, tc.complete)
			}
			if reduced.EventCount != tc.wantCount {
				t.Fatalf("EventCount = %d, want %d", reduced.EventCount, tc.wantCount)
			}
		})
	}
}

// TestReplayTraceBundleValidatesPayloadBodiesLikeRust covers the second half of
// Rust #58172: referenced payloads must exist and be complete UTF-8 JSON.
func TestReplayTraceBundleValidatesPayloadBodiesLikeRust(t *testing.T) {
	event := `{"seq":1,"type":"inference_started","payload":{"type":"inference_started","event_payload":{"raw_payload_id":"payload-1","kind":{"type":"inference_request"},"path":"payloads/payload-1.json"}}}` + "\n"
	cases := []struct {
		name    string
		body    []byte
		missing bool
		wantErr bool
	}{
		{name: "valid", body: []byte(`{"model":"gpt"}`)},
		{name: "truncated json", body: []byte(`{"truncated":`), wantErr: true},
		{name: "invalid utf8", body: []byte("{\"value\":\"\xff\"}"), wantErr: true},
		{name: "missing file", missing: true, wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "manifest.json"), []byte(`{"trace_id":"trace-1"}`), 0o600); err != nil {
				t.Fatalf("WriteFile manifest error = %v", err)
			}
			if err := os.WriteFile(filepath.Join(dir, "trace.jsonl"), []byte(event), 0o600); err != nil {
				t.Fatalf("WriteFile trace error = %v", err)
			}
			if !tc.missing {
				if err := os.MkdirAll(filepath.Join(dir, "payloads"), 0o755); err != nil {
					t.Fatalf("MkdirAll error = %v", err)
				}
				if err := os.WriteFile(filepath.Join(dir, "payloads", "payload-1.json"), tc.body, 0o600); err != nil {
					t.Fatalf("WriteFile payload error = %v", err)
				}
			}
			_, err := ReplayTraceBundle(dir)
			if tc.wantErr {
				if err == nil {
					t.Fatal("ReplayTraceBundle() accepted an invalid payload")
				}
				if !strings.Contains(err.Error(), "payload") {
					t.Fatalf("error = %v, want payload context", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("ReplayTraceBundle() error = %v", err)
			}
		})
	}
}
