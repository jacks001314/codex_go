package rollout

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Mirrors Rust's `compression::read_metrics`: one `codex.rollout_compression.read`
// observation per reader, tagged with the representation, the outcome, and (for
// failures) the stage and bounded error kind, plus the io duration.
func TestOpenRolloutLineReaderRecordsReadMetricsLikeRust(t *testing.T) {
	dir := t.TempDir()
	plain := filepath.Join(dir, "rollout-2025-01-01T00-00-00-reader.jsonl")
	writeTestRollout(t, plain, "reader", "")

	recorder := installRolloutMetricsRecorder(t)

	// A complete plain read reports eof/plain.
	reader, err := OpenRolloutLineReader(plain)
	if err != nil {
		t.Fatalf("OpenRolloutLineReader(plain) error = %v", err)
	}
	readAllLines(t, reader)
	if err := reader.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	assertReadMetric(t, recorder, map[string]string{
		"format": "plain", "outcome": "eof", "stage": "none", "error_kind": "none",
	})

	// A reader released before EOF reports partial.
	partial, err := OpenRolloutLineReader(plain)
	if err != nil {
		t.Fatalf("OpenRolloutLineReader(partial) error = %v", err)
	}
	if err := partial.Close(); err != nil {
		t.Fatalf("partial Close() error = %v", err)
	}
	assertReadMetric(t, recorder, map[string]string{
		"format": "plain", "outcome": "partial", "stage": "none", "error_kind": "none",
	})

	// The compressed representation reports zstd.
	compressed := filepath.Join(dir, "rollout-2025-01-01T00-00-00-zstd.jsonl.zst")
	writeCompressedTestRollout(t, plain, compressed)
	zstdReader, err := OpenRolloutLineReader(compressed)
	if err != nil {
		t.Fatalf("OpenRolloutLineReader(zstd) error = %v", err)
	}
	readAllLines(t, zstdReader)
	if err := zstdReader.Close(); err != nil {
		t.Fatalf("zstd Close() error = %v", err)
	}
	assertReadMetric(t, recorder, map[string]string{
		"format": "zstd", "outcome": "eof", "stage": "none", "error_kind": "none",
	})

	// A missing rollout reports a failed open with the bounded error kind.
	missing := filepath.Join(dir, "rollout-2025-01-01T00-00-00-missing.jsonl")
	if _, err := OpenRolloutLineReader(missing); err == nil {
		t.Fatal("OpenRolloutLineReader(missing) succeeded")
	}
	assertReadMetric(t, recorder, map[string]string{
		"format": "unknown", "outcome": "failed", "stage": "open", "error_kind": "not_found",
	})

	// Every observation also carried the io duration with the same tags.
	for _, tags := range recorder.durationSamples(rolloutReadDurationMetric) {
		if tags["format"] == "unknown" && tags["outcome"] == "failed" && tags["error_kind"] == "not_found" {
			return
		}
	}
	t.Fatalf("read durations = %#v", recorder.durationSamples(rolloutReadDurationMetric))
}

// Mirrors Rust's representation-transition retry: a rollout that appears while
// the reader is retrying opens successfully, and a path that stays missing fails
// once the retry budget is exhausted.
func TestOpenRolloutLineReaderRetriesRepresentationTransitionLikeRust(t *testing.T) {
	dir := t.TempDir()
	appearing := filepath.Join(dir, "rollout-2025-01-01T00-00-00-appearing.jsonl")
	go func() {
		time.Sleep(10 * time.Millisecond)
		writeTestRollout(t, appearing, "appearing", "")
	}()
	reader, err := OpenRolloutLineReader(appearing)
	if err != nil {
		t.Fatalf("OpenRolloutLineReader(appearing) error = %v, want a retried open", err)
	}
	if err := reader.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	// A path that never appears fails after the bounded retries rather than
	// blocking forever.
	never := filepath.Join(dir, "rollout-2025-01-01T00-00-00-never.jsonl")
	startedAt := time.Now()
	if _, err := OpenRolloutLineReader(never); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("OpenRolloutLineReader(never) error = %v, want not-found", err)
	}
	if elapsed := time.Since(startedAt); elapsed < rolloutLineReaderRetryDelay {
		t.Fatalf("retry budget elapsed = %v, want at least one retry delay", elapsed)
	}
}

// Mirrors Rust's `Load`-side behaviour: `Load` resolves the current
// representation, so a compressed sibling is read without the caller knowing.
func TestLoadReadsCompressedSiblingLikeRust(t *testing.T) {
	dir := t.TempDir()
	plain := filepath.Join(dir, "rollout-2025-01-01T00-00-00-load.jsonl")
	writeTestRollout(t, plain, "load", "")
	compressed := plain + ".zst"
	writeCompressedTestRollout(t, plain, compressed)
	if err := os.Remove(plain); err != nil {
		t.Fatalf("Remove(plain): %v", err)
	}
	if resolved, ok := ExistingRolloutPath(plain); !ok || resolved != compressed {
		t.Fatalf("ExistingRolloutPath(%q) = %q/%v, want %q", plain, resolved, ok, compressed)
	}

	lines, parseErrors, err := Load(plain)
	if err != nil {
		t.Fatalf("Load(compressed sibling) error = %v", err)
	}
	if parseErrors != 0 || len(lines) == 0 {
		t.Fatalf("Load() = %d lines, %d parse errors", len(lines), parseErrors)
	}
}

func readAllLines(t *testing.T, reader *RolloutLineReader) {
	t.Helper()
	for {
		_, err := reader.ReadLine()
		if errors.Is(err, io.EOF) {
			return
		}
		if err != nil {
			t.Fatalf("ReadLine() error = %v", err)
		}
	}
}

func writeCompressedTestRollout(t *testing.T, source string, destination string) {
	t.Helper()
	file, err := os.Create(destination)
	if err != nil {
		t.Fatalf("Create(%s): %v", destination, err)
	}
	if err := encodeRolloutZstd(source, file); err != nil {
		_ = file.Close()
		t.Fatalf("encodeRolloutZstd: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("Close(%s): %v", destination, err)
	}
}

func assertReadMetric(t *testing.T, recorder *rolloutMetricsRecorder, want map[string]string) {
	t.Helper()
	if samples := recorder.samples(rolloutReadCounter); !hasRolloutMetricSample(samples, want) {
		t.Fatalf("read counters = %#v, want %#v", samples, want)
	}
}
