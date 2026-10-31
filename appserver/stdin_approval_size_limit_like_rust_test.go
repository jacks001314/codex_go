package appserver

import (
	"strings"
	"testing"
	"time"

	"codex_go/metrics"
	"codex_go/state"
)

type stdinReviewMetricSample struct {
	result    string
	inputKind string
}

type stdinReviewRecorder struct {
	samples []stdinReviewMetricSample
}

func (r *stdinReviewRecorder) RecordDuration(string, time.Duration, map[string]string) {}

func (r *stdinReviewRecorder) Counter(name string, _ int, tags map[string]string) {
	if name != stdinReviewSizeCheckMetric {
		return
	}
	r.samples = append(r.samples, stdinReviewMetricSample{result: tags["result"], inputKind: tags["input_kind"]})
}

func (r *stdinReviewRecorder) Histogram(string, int, map[string]string) {}

func installStdinReviewRecorder(t *testing.T) *stdinReviewRecorder {
	t.Helper()
	recorder := &stdinReviewRecorder{}
	previous := metrics.Global()
	metrics.InstallGlobal(recorder)
	t.Cleanup(func() { metrics.InstallGlobal(previous) })
	return recorder
}

// Mirrors Rust's unified-exec stdin approval bound (#44060): terminal input is
// reviewed under the separate 8,000-byte cap even though the general Guardian
// action limit is 200,000 bytes, and the size-check metric names why.
func TestGuardianStdinReviewSizeLimitLikeRust(t *testing.T) {
	const unreviewable = "terminal input and permission details are too large to review safely; use a smaller input or start a new terminal with fewer grants"
	recorder := installStdinReviewRecorder(t)

	small := state.Action{Type: "write_stdin", SessionID: intPtr(1), Chars: "ls\n"}
	if err := guardStdinReviewSize(small, "This terminal uses the current permissions."); err != nil {
		t.Fatalf("small stdin review error = %v, want accepted", err)
	}

	control := state.Action{Type: "write_stdin", SessionID: intPtr(1), Chars: "\u0003"}
	if err := guardStdinReviewSize(control, ""); err != nil {
		t.Fatalf("control stdin review error = %v, want accepted", err)
	}

	oversized := state.Action{Type: "write_stdin", SessionID: intPtr(1), Chars: strings.Repeat("x", maxStdinApprovalBytes+1)}
	if err := guardStdinReviewSize(oversized, ""); err == nil || err.Error() != unreviewable {
		t.Fatalf("oversized stdin review error = %v, want %q", err, unreviewable)
	}

	// Rust #44569 removed the general formatter truncation and byte limit, so a
	// very long terminal input is measured by this stdin gate alone.
	large := state.Action{Type: "write_stdin", SessionID: intPtr(1), Chars: strings.Repeat("y", 100_000)}
	if err := guardStdinReviewSize(large, ""); err == nil || err.Error() != unreviewable {
		t.Fatalf("large stdin review error = %v, want %q", err, unreviewable)
	}

	// An aggregate action beyond the general Guardian limit is still measured
	// here; the formatter now renders it completely.
	files := make([]string, 20_000)
	for index := range files {
		files[index] = "/tmp/file"
	}
	if err := guardStdinReviewSize(state.Action{Type: "apply_patch", CWD: "/tmp", Files: files}, ""); err == nil || err.Error() != unreviewable {
		t.Fatalf("aggregate stdin review error = %v, want %q", err, unreviewable)
	}

	want := []stdinReviewMetricSample{
		{result: "within_limit", inputKind: "text"},
		{result: "within_limit", inputKind: "control"},
		{result: "over_limit", inputKind: "text"},
		{result: "over_limit", inputKind: "text"},
		{result: "over_limit", inputKind: "control"},
	}
	if len(recorder.samples) != len(want) {
		t.Fatalf("size-check samples = %#v, want %#v", recorder.samples, want)
	}
	for index := range want {
		if recorder.samples[index] != want[index] {
			t.Fatalf("size-check sample %d = %#v, want %#v", index, recorder.samples[index], want[index])
		}
	}
}
