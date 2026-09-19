package rollout

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"testing"
	"time"

	"codex_go/metrics"
)

type rolloutMetricSample struct {
	name string
	tags map[string]string
}

type rolloutMetricsRecorder struct {
	mu         sync.Mutex
	counters   []rolloutMetricSample
	histograms []rolloutMetricSample
	durations  []rolloutMetricSample
}

func (r *rolloutMetricsRecorder) RecordDuration(name string, _ time.Duration, tags map[string]string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.durations = append(r.durations, rolloutMetricSample{name: name, tags: cloneRolloutMetricTags(tags)})
}

func (r *rolloutMetricsRecorder) Counter(name string, _ int, tags map[string]string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.counters = append(r.counters, rolloutMetricSample{name: name, tags: cloneRolloutMetricTags(tags)})
}

func (r *rolloutMetricsRecorder) Histogram(name string, _ int, tags map[string]string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.histograms = append(r.histograms, rolloutMetricSample{name: name, tags: cloneRolloutMetricTags(tags)})
}

func (r *rolloutMetricsRecorder) samples(name string) []map[string]string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := []map[string]string{}
	for _, sample := range r.counters {
		if sample.name == name {
			out = append(out, sample.tags)
		}
	}
	return out
}

func (r *rolloutMetricsRecorder) histogramSamples(name string) []map[string]string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := []map[string]string{}
	for _, sample := range r.histograms {
		if sample.name == name {
			out = append(out, sample.tags)
		}
	}
	return out
}

func (r *rolloutMetricsRecorder) durationSamples(name string) []map[string]string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := []map[string]string{}
	for _, sample := range r.durations {
		if sample.name == name {
			out = append(out, sample.tags)
		}
	}
	return out
}

func cloneRolloutMetricTags(tags map[string]string) map[string]string {
	cloned := make(map[string]string, len(tags))
	for key, value := range tags {
		cloned[key] = value
	}
	return cloned
}

func installRolloutMetricsRecorder(t *testing.T) *rolloutMetricsRecorder {
	t.Helper()
	recorder := &rolloutMetricsRecorder{}
	metrics.InstallGlobal(recorder)
	t.Cleanup(func() { metrics.InstallGlobal(nil) })
	return recorder
}

func hasRolloutMetricSample(samples []map[string]string, want map[string]string) bool {
	for _, sample := range samples {
		matches := true
		for key, value := range want {
			if sample[key] != value {
				matches = false
				break
			}
		}
		if matches {
			return true
		}
	}
	return false
}

// Mirrors Rust's rollout compression metrics: a pass reports its status, the
// completion tags, and the per-file outcome, duration, byte and ratio
// observations.
func TestRolloutCompressionWorkerMetricsLikeRust(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, SessionsSubdir)
	coldPath := filepath.Join(root, "rollout-2025-01-01T00-00-00-cold.jsonl")
	writeTestRollout(t, coldPath, "cold", "")
	coldTime := time.Now().Add(-8 * 24 * time.Hour)
	if err := os.Chtimes(coldPath, coldTime, coldTime); err != nil {
		t.Fatalf("Chtimes: %v", err)
	}

	recorder := installRolloutMetricsRecorder(t)
	if err := RunRolloutCompression(context.Background(), home, RolloutCompressionTriggerStartup); err != nil {
		t.Fatalf("RunRolloutCompression() error = %v", err)
	}

	runSamples := recorder.samples(rolloutCompressionRunCounter)
	if !hasRolloutMetricSample(runSamples, map[string]string{"status": "started", "trigger": "startup"}) {
		t.Fatalf("run counters = %#v, want a started pass", runSamples)
	}
	if !hasRolloutMetricSample(runSamples, map[string]string{
		"status":            "completed",
		"trigger":           "startup",
		"completion_reason": "scan_finished",
		"file_errors":       "false",
		"scan_errors":       "false",
		"cleanup_errors":    "false",
	}) {
		t.Fatalf("run counters = %#v, want a completed pass with the Rust tags", runSamples)
	}
	if samples := recorder.durationSamples(rolloutCompressionRunDurationMetric); !hasRolloutMetricSample(samples, map[string]string{"status": "completed", "trigger": "startup"}) {
		t.Fatalf("run durations = %#v", samples)
	}

	fileSamples := recorder.samples(rolloutCompressionFileCounter)
	if !hasRolloutMetricSample(fileSamples, map[string]string{"outcome": "scanned", "trigger": "startup"}) {
		t.Fatalf("file counters = %#v, want a scanned rollout", fileSamples)
	}
	if !hasRolloutMetricSample(fileSamples, map[string]string{"outcome": "compressed", "trigger": "startup"}) {
		t.Fatalf("file counters = %#v, want a compressed rollout", fileSamples)
	}
	for _, name := range []string{rolloutCompressionFileSourceBytes, rolloutCompressionFileCompressedBytes, rolloutCompressionFileRatioMetric} {
		if samples := recorder.histogramSamples(name); !hasRolloutMetricSample(samples, map[string]string{"outcome": "compressed", "trigger": "startup"}) {
			t.Fatalf("%s histograms = %#v", name, samples)
		}
	}
	if samples := recorder.durationSamples(rolloutCompressionFileDurationMetric); !hasRolloutMetricSample(samples, map[string]string{"outcome": "compressed", "trigger": "startup"}) {
		t.Fatalf("file durations = %#v", samples)
	}

	// A second pass is skipped by the run marker.
	if err := RunRolloutCompression(context.Background(), home, RolloutCompressionTriggerRPC); err != nil {
		t.Fatalf("second RunRolloutCompression() error = %v", err)
	}
	if samples := recorder.samples(rolloutCompressionRunCounter); !hasRolloutMetricSample(samples, map[string]string{"status": "skipped_already_running", "trigger": "rpc"}) {
		t.Fatalf("run counters after the second pass = %#v", samples)
	}
}

// Mirrors Rust's stale temp cleanup: temp files older than the run marker's
// stale window are removed and counted, fresh ones are kept, and the pass
// reports whether cleanup errors occurred.
func TestRolloutCompressionTempCleanupMetricsLikeRust(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, SessionsSubdir)
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	stale := filepath.Join(root, "rollout-compress-stale.tmp")
	fresh := filepath.Join(root, "rollout-compress-fresh.tmp")
	for _, path := range []string{stale, fresh} {
		if err := os.WriteFile(path, []byte("partial"), 0o600); err != nil {
			t.Fatalf("WriteFile(%s): %v", path, err)
		}
	}
	old := time.Now().Add(-7 * time.Hour)
	if err := os.Chtimes(stale, old, old); err != nil {
		t.Fatalf("Chtimes: %v", err)
	}

	recorder := installRolloutMetricsRecorder(t)
	if err := RunRolloutCompression(context.Background(), home, RolloutCompressionTriggerStartup); err != nil {
		t.Fatalf("RunRolloutCompression() error = %v", err)
	}

	if _, err := os.Stat(stale); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stale temp file was not removed (err=%v)", err)
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Fatalf("fresh temp file was removed: %v", err)
	}
	if samples := recorder.samples(rolloutCompressionTempCleanupCounter); !hasRolloutMetricSample(samples, map[string]string{"outcome": "removed", "trigger": "startup"}) {
		t.Fatalf("temp cleanup counters = %#v", samples)
	}
	if samples := recorder.samples(rolloutCompressionRunCounter); !hasRolloutMetricSample(samples, map[string]string{
		"status": "completed", "trigger": "startup", "cleanup_errors": "false",
	}) {
		t.Fatalf("run counters = %#v", samples)
	}
}

// Mirrors Rust's materialize metrics: an existing plain rollout reports
// `plain_exists`, a missing pair reports `missing`, and a decode reports
// `decompressed` with its duration.
func TestMaterializeRolloutReferenceMetricsLikeRust(t *testing.T) {
	dir := t.TempDir()
	plain := filepath.Join(dir, "rollout-2025-01-01T00-00-00-metrics.jsonl")
	writeTestRollout(t, plain, "metrics", "")
	compressed := plain + ".zst"
	file, err := os.Create(compressed)
	if err != nil {
		t.Fatalf("Create(%s): %v", compressed, err)
	}
	if err := encodeRolloutZstd(plain, file); err != nil {
		_ = file.Close()
		t.Fatalf("encodeRolloutZstd: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := os.Remove(plain); err != nil {
		t.Fatalf("Remove(plain): %v", err)
	}

	recorder := installRolloutMetricsRecorder(t)
	if _, err := MaterializeRolloutForReference(compressed); err != nil {
		t.Fatalf("MaterializeRolloutForReference() error = %v", err)
	}
	if samples := recorder.samples(rolloutCompressionMaterializeCounter); !hasRolloutMetricSample(samples, map[string]string{"outcome": "decompressed"}) {
		t.Fatalf("materialize counters = %#v, want decompressed", samples)
	}
	if samples := recorder.durationSamples(rolloutCompressionMaterializeDurationMetric); !hasRolloutMetricSample(samples, map[string]string{"outcome": "decompressed"}) {
		t.Fatalf("materialize durations = %#v", samples)
	}
	// The plain file now exists, so the next call is a no-op.
	if _, err := MaterializeRolloutForReference(compressed); err != nil {
		t.Fatalf("second MaterializeRolloutForReference() error = %v", err)
	}
	if samples := recorder.samples(rolloutCompressionMaterializeCounter); !hasRolloutMetricSample(samples, map[string]string{"outcome": "plain_exists"}) {
		t.Fatalf("materialize counters = %#v, want plain_exists", samples)
	}
	// Neither representation present.
	missing := filepath.Join(dir, "rollout-2025-01-01T00-00-00-missing.jsonl")
	if _, err := MaterializeRolloutForReference(missing); err != nil {
		t.Fatalf("missing MaterializeRolloutForReference() error = %v", err)
	}
	if samples := recorder.samples(rolloutCompressionMaterializeCounter); !hasRolloutMetricSample(samples, map[string]string{"outcome": "missing"}) {
		t.Fatalf("materialize counters = %#v, want missing", samples)
	}
}

// Mirrors Rust's `error_kind`: a fixed set of categories so messages and paths
// never become metric tags.
func TestRolloutCompressionErrorKindLikeRust(t *testing.T) {
	cases := []struct {
		err  error
		want string
	}{
		{os.ErrNotExist, "not_found"},
		{os.ErrPermission, "permission_denied"},
		{os.ErrExist, "already_exists"},
		{os.ErrInvalid, "invalid_input"},
		{context.DeadlineExceeded, "timed_out"},
		{io.ErrUnexpectedEOF, "unexpected_eof"},
		{io.ErrShortWrite, "write_zero"},
		{errors.ErrUnsupported, "unsupported"},
		{syscall.ENOSPC, "storage_full"},
		{syscall.EROFS, "read_only_filesystem"},
		{syscall.ENOTDIR, "not_a_directory"},
		{syscall.EISDIR, "is_a_directory"},
		{errors.New("boom"), "other"},
	}
	for _, tc := range cases {
		if got := rolloutCompressionErrorKind(tc.err); got != tc.want {
			t.Fatalf("rolloutCompressionErrorKind(%v) = %q, want %q", tc.err, got, tc.want)
		}
	}

	// The failure counters carry the outcome key, stage, error kind and - only
	// for the trigger-scoped families - the trigger.
	recorder := installRolloutMetricsRecorder(t)
	trigger := RolloutCompressionTriggerRPC
	rolloutCompressionFailure(rolloutCompressionFileCounter, "outcome", &trigger, "encode", os.ErrPermission)
	if samples := recorder.samples(rolloutCompressionFileCounter); !hasRolloutMetricSample(samples, map[string]string{
		"outcome": "failed", "stage": "encode", "error_kind": "permission_denied", "trigger": "rpc",
	}) {
		t.Fatalf("file failure counters = %#v", samples)
	}
	rolloutCompressionFailure(rolloutCompressionMaterializeCounter, "outcome", nil, "create_temp", os.ErrNotExist)
	if samples := recorder.samples(rolloutCompressionMaterializeCounter); !hasRolloutMetricSample(samples, map[string]string{
		"outcome": "failed", "stage": "create_temp", "error_kind": "not_found",
	}) {
		t.Fatalf("materialize failure counters = %#v", samples)
	}
	for _, sample := range recorder.samples(rolloutCompressionMaterializeCounter) {
		if _, ok := sample["trigger"]; ok {
			t.Fatalf("materialize failure carried a trigger tag: %#v", sample)
		}
	}
}
