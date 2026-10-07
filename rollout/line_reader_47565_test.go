package rollout

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/klauspost/compress/zstd"
)

// Mirrors Rust #47565: the read-failure observation carries the bounded
// `reason` and `read_progress` labels beside the stage and error kind, and the
// successful eof/partial outcomes carry `none` for both.
func TestRolloutReadFailureReasonAndProgressLabelsLikeRust(t *testing.T) {
	dir := t.TempDir()

	// A `.jsonl.zst` whose bytes are not a zstd frame at all fails before any
	// line is read: reason=zstd_invalid_frame, read_progress=before_first_line.
	garbage := filepath.Join(dir, "rollout-2025-01-01T00-00-00-garbage.jsonl.zst")
	if err := os.WriteFile(garbage, []byte("this is definitely not a zstd frame"), 0o600); err != nil {
		t.Fatalf("WriteFile(garbage): %v", err)
	}

	// A valid first frame followed by trailing garbage yields one line, then a
	// frame-header failure: read_progress=after_first_line.
	firstFrame := zstdFrameOf(t, dir, "first", "{\"i\":1}\n")
	mixed := filepath.Join(dir, "rollout-2025-01-01T00-00-00-mixed.jsonl.zst")
	if err := os.WriteFile(mixed, append(append([]byte{}, firstFrame...), []byte("GARBAGE-GARBAGE")...), 0o600); err != nil {
		t.Fatalf("WriteFile(mixed): %v", err)
	}

	// A truncated frame ends early: the decoder reports io.ErrUnexpectedEOF,
	// which has no zstd message entry and therefore stays stream_error.
	truncated := filepath.Join(dir, "rollout-2025-01-01T00-00-00-truncated.jsonl.zst")
	if err := os.WriteFile(truncated, firstFrame[:len(firstFrame)-3], 0o600); err != nil {
		t.Fatalf("WriteFile(truncated): %v", err)
	}

	plain := filepath.Join(dir, "rollout-2025-01-01T00-00-00-plain.jsonl")
	writeTestRollout(t, plain, "plain", "")

	recorder := installRolloutMetricsRecorder(t)

	readUntilError(t, garbage)
	assertReadMetricTags(t, recorder, map[string]string{
		"format": "zstd", "outcome": "failed", "stage": "read",
		"reason": "zstd_invalid_frame", "read_progress": "before_first_line",
	})

	readUntilError(t, mixed)
	assertReadMetricTags(t, recorder, map[string]string{
		"format": "zstd", "outcome": "failed", "stage": "read",
		"reason": "zstd_invalid_frame", "read_progress": "after_first_line",
	})

	readUntilError(t, truncated)
	assertReadMetricTags(t, recorder, map[string]string{
		"format": "zstd", "outcome": "failed", "stage": "read",
		"error_kind": "unexpected_eof", "reason": "stream_error",
		"read_progress": "before_first_line",
	})

	// A completed plain read keeps the placeholder labels for both new tags.
	complete, err := OpenRolloutLineReader(plain)
	if err != nil {
		t.Fatalf("OpenRolloutLineReader(plain) error = %v", err)
	}
	readAllLines(t, complete)
	if err := complete.Close(); err != nil {
		t.Fatalf("Close(plain) error = %v", err)
	}
	assertReadMetricTags(t, recorder, map[string]string{
		"format": "plain", "outcome": "eof", "stage": "none", "error_kind": "none",
		"reason": "none", "read_progress": "none",
	})

	// A reader abandoned before EOF keeps the placeholder labels too.
	partial, err := OpenRolloutLineReader(plain)
	if err != nil {
		t.Fatalf("OpenRolloutLineReader(partial) error = %v", err)
	}
	if err := partial.Close(); err != nil {
		t.Fatalf("Close(partial) error = %v", err)
	}
	assertReadMetricTags(t, recorder, map[string]string{
		"format": "plain", "outcome": "partial", "stage": "none", "error_kind": "none",
		"reason": "none", "read_progress": "none",
	})

	// A failed open is an OS error, recorded with the before-first-line progress.
	missing := filepath.Join(dir, "rollout-2025-01-01T00-00-00-missing.jsonl")
	if _, err := OpenRolloutLineReader(missing); err == nil {
		t.Fatal("OpenRolloutLineReader(missing) succeeded")
	}
	assertReadMetricTags(t, recorder, map[string]string{
		"format": "unknown", "outcome": "failed", "stage": "open",
		"error_kind": "not_found", "reason": "os_error",
		"read_progress": "before_first_line",
	})
}

// The Go decoder reports its own error identities; the reason table must be
// derived from those, not from Rust's zstd crate messages.
func TestRolloutReadZstdReasonMapsGoDecoderErrors(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want string
	}{
		{"magic mismatch", zstd.ErrMagicMismatch, rolloutReadReasonZstdInvalidFrame},
		{"window size too small", zstd.ErrWindowSizeTooSmall, rolloutReadReasonZstdInvalidFrame},
		{"reserved block type", zstd.ErrReservedBlockType, rolloutReadReasonZstdInvalidFrame},
		{"block too small", zstd.ErrBlockTooSmall, rolloutReadReasonZstdCorruptBlock},
		{"unexpected block size", zstd.ErrUnexpectedBlockSize, rolloutReadReasonZstdCorruptBlock},
		{"compressed size too big", zstd.ErrCompressedSizeTooBig, rolloutReadReasonZstdCorruptBlock},
		{"crc mismatch", zstd.ErrCRCMismatch, rolloutReadReasonZstdChecksum},
		{"decoder size exceeded", zstd.ErrDecoderSizeExceeded, rolloutReadReasonZstdResourceLimit},
		{"window size exceeded", zstd.ErrWindowSizeExceeded, rolloutReadReasonZstdResourceLimit},
		{"frame size exceeded", zstd.ErrFrameSizeExceeded, rolloutReadReasonZstdResourceLimit},
		{"frame size mismatch", zstd.ErrFrameSizeMismatch, rolloutReadReasonZstdResourceLimit},
		{"unknown dictionary", zstd.ErrUnknownDictionary, rolloutReadReasonZstdDictionary},
		// A truncated stream surfaces io.ErrUnexpectedEOF from the decoder and
		// has no zstd message entry: it stays the generic stream error.
		{"unexpected eof", io.ErrUnexpectedEOF, ""},
		{"unfamiliar message", errors.New("some other decoder complaint"), ""},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := rolloutReadZstdReason(testCase.err); got != testCase.want {
				t.Fatalf("rolloutReadZstdReason(%v) = %q, want %q", testCase.err, got, testCase.want)
			}
		})
	}
}

// The wrapper only consults the zstd table for a `.zst` representation, and it
// prefers the OS-errno classification over everything else.
func TestRolloutReadFailureReasonPrefersOSErrnoAndFormat(t *testing.T) {
	pathErr := &os.PathError{Op: "open", Path: "x", Err: syscall.ENOENT}
	cases := []struct {
		name   string
		format string
		err    error
		want   string
	}{
		{"os errno on plain", rolloutReadFormatPlain, pathErr, rolloutReadReasonOSError},
		{"os errno on unknown format", rolloutReadFormatUnknown, pathErr, rolloutReadReasonOSError},
		{"os errno wins over zstd", rolloutReadFormatZstd, pathErr, rolloutReadReasonOSError},
		{"zstd frame error needs zstd format", rolloutReadFormatZstd, zstd.ErrMagicMismatch, rolloutReadReasonZstdInvalidFrame},
		{"zstd frame error on plain format is a stream error", rolloutReadFormatPlain, zstd.ErrMagicMismatch, rolloutReadReasonStreamError},
		{"nil error is a stream error", rolloutReadFormatPlain, nil, rolloutReadReasonStreamError},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := rolloutReadFailureReason(testCase.format, testCase.err); got != testCase.want {
				t.Fatalf("rolloutReadFailureReason(%q, %v) = %q, want %q", testCase.format, testCase.err, got, testCase.want)
			}
		})
	}
	if !rolloutReadErrorHasOSErrno(pathErr) {
		t.Fatal("rolloutReadErrorHasOSErrno(os.PathError) = false, want true")
	}
	if rolloutReadErrorHasOSErrno(io.ErrUnexpectedEOF) {
		t.Fatal("rolloutReadErrorHasOSErrno(io.ErrUnexpectedEOF) = true, want false")
	}
}

func readUntilError(t *testing.T, path string) {
	t.Helper()
	reader, err := OpenRolloutLineReader(path)
	if err != nil {
		t.Fatalf("OpenRolloutLineReader(%s) error = %v", path, err)
	}
	for {
		if _, err := reader.ReadLine(); err != nil {
			if errors.Is(err, io.EOF) {
				t.Fatalf("ReadLine(%s) reached EOF without the expected failure", path)
			}
			break
		}
	}
	if err := reader.Close(); err != nil {
		t.Fatalf("Close(%s) error = %v", path, err)
	}
}

func zstdFrameOf(t *testing.T, dir string, name string, content string) []byte {
	t.Helper()
	plain := filepath.Join(dir, name+".jsonl")
	if err := os.WriteFile(plain, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile(%s): %v", plain, err)
	}
	var buffer bytes.Buffer
	if err := encodeRolloutZstd(plain, &buffer); err != nil {
		t.Fatalf("encodeRolloutZstd(%s): %v", plain, err)
	}
	if buffer.Len() == 0 || !strings.HasPrefix(buffer.String(), "\x28\xb5\x2f\xfd") {
		t.Fatalf("zstdFrameOf(%s) did not produce a zstd frame", name)
	}
	return buffer.Bytes()
}

func assertReadMetricTags(t *testing.T, recorder *rolloutMetricsRecorder, want map[string]string) {
	t.Helper()
	if samples := recorder.samples(rolloutReadCounter); !hasRolloutMetricSample(samples, want) {
		t.Fatalf("read counters = %#v, want %#v", samples, want)
	}
}
