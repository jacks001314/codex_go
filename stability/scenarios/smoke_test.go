//go:build stability

package scenarios

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"codex_go/model"
	"codex_go/stability/harness/disk"
	"codex_go/stability/harness/metrics"
	"codex_go/stability/harness/mockmodel"
	"codex_go/stability/harness/report"
)

func TestMockModelSmoke(t *testing.T) {
	const want = "hello from stability"
	server := mockmodel.New(mockmodel.Text(want))
	defer server.Close()

	runner := newRunner(server.URL())
	response, err := runner.Run(context.Background(), &model.AgentRequest{Prompt: "ping", Model: "gpt-mock"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if response.Message != want {
		t.Fatalf("message = %q, want %q", response.Message, want)
	}
	requests := server.Requests()
	if len(requests) != 1 {
		t.Fatalf("requests = %d, want 1", len(requests))
	}
	if !strings.Contains(string(requests[0].Body), `"stream":true`) {
		t.Fatalf("request was not streaming: %s", requests[0].Body)
	}
}

func TestMockModelGateBlocksThenReleases(t *testing.T) {
	gate := mockmodel.NewGate()
	server := mockmodel.New(mockmodel.Text("gated").GateAt(2, gate))
	defer server.Close()

	runner := newRunner(server.URL())
	done := make(chan error, 1)
	go func() {
		_, err := runner.Run(context.Background(), &model.AgentRequest{Prompt: "ping", Model: "gpt-mock"})
		done <- err
	}()

	select {
	case err := <-done:
		t.Fatalf("run finished before the gate was released: %v", err)
	case <-time.After(150 * time.Millisecond):
	}

	gate.Release()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("run failed after release: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("run did not finish after the gate was released")
	}
}

func TestMetricsDiskAndReportSmoke(t *testing.T) {
	run := report.New("smoke")
	sample := metrics.Take()
	if sample.Goroutines <= 0 {
		t.Fatalf("goroutines = %d, want > 0", sample.Goroutines)
	}

	root := t.TempDir()
	payload := filepath.Join(root, "rollout.jsonl")
	if err := os.WriteFile(payload, []byte("line\n"), 0o600); err != nil {
		t.Fatalf("write payload: %v", err)
	}
	snapshot, err := disk.Take(root)
	if err != nil {
		t.Fatalf("disk.Take: %v", err)
	}
	if snapshot.Files != 1 || snapshot.TotalBytes != 5 {
		t.Fatalf("disk snapshot = %+v, want 1 file and 5 bytes", snapshot)
	}

	run.AddSample(sample)
	run.AddDisk(snapshot)
	run.Check("sample.goroutines", sample.Goroutines > 0, "goroutines=%d", sample.Goroutines)
	run.Check("disk.files", snapshot.Files == 1, "files=%d", snapshot.Files)
	run.Finish()
	if !run.Passed {
		t.Fatalf("report failed: %+v", run.Checks)
	}
	path, err := run.Write(reportDir())
	if err != nil {
		t.Fatalf("write report: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("report missing: %v", err)
	}
}

func newRunner(baseURL string) *model.ResponsesAgentRunner {
	return model.NewResponsesAgentRunner(&model.ResponsesAgentOptions{
		Provider: &model.APIProvider{BaseURL: baseURL + "/v1"},
		Stream:   true,
	})
}
