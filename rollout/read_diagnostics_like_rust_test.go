package rollout

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/klauspost/compress/zstd"
)

// Mirrors Rust #55545 `compression::read_diagnostics_tests`: byte counts and
// file changes are captured on the first compressed-stream failure, and the
// warning omits paths and contents.

// singleSegmentZstd encodes contents with a declared frame content size, the
// way Rust's `set_pledged_src_size` writer does.
func singleSegmentZstd(t *testing.T, contents []byte) []byte {
	t.Helper()
	encoder, err := zstd.NewWriter(nil, zstd.WithSingleSegment(true))
	if err != nil {
		t.Fatalf("zstd.NewWriter() error = %v", err)
	}
	defer encoder.Close()
	return encoder.EncodeAll(contents, nil)
}

// readDiagnosticRolloutFile decodes a compressed rollout through the
// diagnostic decoder, optionally truncating the file after it is opened.
func readDiagnosticRolloutFile(t *testing.T, path string, truncateTo int64) ([]byte, error, *compressionReadFailure) {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatalf("os.Open(%q) error = %v", path, err)
	}
	decoder, err := newDiagnosticZstdDecoder(file)
	if err != nil {
		_ = file.Close()
		t.Fatalf("newDiagnosticZstdDecoder() error = %v", err)
	}
	if truncateTo > 0 {
		if err := os.Truncate(path, truncateTo); err != nil {
			_ = file.Close()
			t.Fatalf("os.Truncate() error = %v", err)
		}
	}
	decoded, readErr := io.ReadAll(decoder)
	decoder.Close()
	if closeErr := file.Close(); closeErr != nil {
		t.Fatalf("file.Close() error = %v", closeErr)
	}
	return decoded, readErr, decoder.failure
}

func TestCompressedRolloutReadDiagnosticsLikeRust(t *testing.T) {
	contents := []byte("valid utf8\n")
	complete := singleSegmentZstd(t, contents)
	if size, declared, ok := firstFrameContentSize(complete); !ok || !declared || size != uint64(len(contents)) {
		t.Fatalf("firstFrameContentSize() = (%d, %v, %v), want (%d, true, true)", size, declared, ok, len(contents))
	}

	t.Run("truncated stream", func(t *testing.T) {
		resetCompressionReadDiagnosticsForTest()
		dir := t.TempDir()
		path := filepath.Join(dir, "rollout.jsonl.zst")
		damaged := complete[:len(complete)-1]
		if err := os.WriteFile(path, damaged, 0o644); err != nil {
			t.Fatalf("os.WriteFile() error = %v", err)
		}
		decoded, readErr, failure := readDiagnosticRolloutFile(t, path, 0)
		if !errors.Is(readErr, io.ErrUnexpectedEOF) {
			t.Fatalf("read error = %v, want io.ErrUnexpectedEOF", readErr)
		}
		if failure == nil {
			t.Fatal("failure details were not captured")
		}
		if failure.compressedBytes == nil || *failure.compressedBytes != int64(len(damaged)) {
			t.Fatalf("compressedBytes = %v, want %d", optionalInt64(failure.compressedBytes), len(damaged))
		}
		if failure.firstFrameContentBytes == nil || *failure.firstFrameContentBytes != uint64(len(contents)) {
			t.Fatalf("firstFrameContentBytes = %v, want %d", optionalUint64(failure.firstFrameContentBytes), len(contents))
		}
		// klauspost/compress withholds a partially received block, so a small
		// single-block stream can report no decoded bytes where Rust's decoder
		// emits the bytes of the truncated block. The count always matches what
		// the caller actually received.
		if failure.decodedBytes != uint64(len(decoded)) {
			t.Fatalf("decodedBytes = %d, want %d", failure.decodedBytes, len(decoded))
		}
		if failure.fileChanged == nil || *failure.fileChanged {
			t.Fatalf("fileChanged = %v, want false", optionalBool(failure.fileChanged))
		}
	})

	t.Run("file truncated after open", func(t *testing.T) {
		resetCompressionReadDiagnosticsForTest()
		var builder bytes.Buffer
		for i := 0; i < 20000; i++ {
			fmt.Fprintf(&builder, "{\"type\":\"event_msg\",\"payload\":{\"type\":\"turn_%d\"}}\n", i)
		}
		largeContents := builder.Bytes()
		largeComplete := singleSegmentZstd(t, largeContents)
		dir := t.TempDir()
		path := filepath.Join(dir, "rollout.jsonl.zst")
		if err := os.WriteFile(path, largeComplete, 0o644); err != nil {
			t.Fatalf("os.WriteFile() error = %v", err)
		}
		truncateTo := int64(len(largeComplete) * 6 / 10)
		decoded, readErr, failure := readDiagnosticRolloutFile(t, path, truncateTo)
		if !errors.Is(readErr, io.ErrUnexpectedEOF) {
			t.Fatalf("read error = %v, want io.ErrUnexpectedEOF", readErr)
		}
		if failure == nil {
			t.Fatal("failure details were not captured")
		}
		if failure.compressedBytes == nil || *failure.compressedBytes != truncateTo {
			t.Fatalf("compressedBytes = %v, want %d", optionalInt64(failure.compressedBytes), truncateTo)
		}
		if failure.firstFrameContentBytes == nil || *failure.firstFrameContentBytes != uint64(len(largeContents)) {
			t.Fatalf("firstFrameContentBytes = %v, want %d", optionalUint64(failure.firstFrameContentBytes), len(largeContents))
		}
		if failure.decodedBytes == 0 || failure.decodedBytes != uint64(len(decoded)) {
			t.Fatalf("decodedBytes = %d, decoded = %d, want a partial decode", failure.decodedBytes, len(decoded))
		}
		if failure.fileChanged == nil || !*failure.fileChanged {
			t.Fatalf("fileChanged = %v, want true", optionalBool(failure.fileChanged))
		}
	})

	t.Run("incomplete trailing frame header", func(t *testing.T) {
		resetCompressionReadDiagnosticsForTest()
		dir := t.TempDir()
		path := filepath.Join(dir, "rollout.jsonl.zst")
		damaged := append(append([]byte(nil), complete...), 0x28)
		if err := os.WriteFile(path, damaged, 0o644); err != nil {
			t.Fatalf("os.WriteFile() error = %v", err)
		}
		decoded, readErr, failure := readDiagnosticRolloutFile(t, path, 0)
		if !errors.Is(readErr, io.ErrUnexpectedEOF) {
			t.Fatalf("read error = %v, want io.ErrUnexpectedEOF", readErr)
		}
		if !bytes.Equal(decoded, contents) {
			t.Fatalf("decoded = %q, want %q", decoded, contents)
		}
		if failure == nil {
			t.Fatal("failure details were not captured")
		}
		if failure.decodedBytes != uint64(len(contents)) {
			t.Fatalf("decodedBytes = %d, want %d", failure.decodedBytes, len(contents))
		}
		if failure.fileChanged == nil || *failure.fileChanged {
			t.Fatalf("fileChanged = %v, want false", optionalBool(failure.fileChanged))
		}
	})
}

// TestCompressedRolloutReadDiagnosticRateLimitLikeRust covers Rust's
// process-wide once-per-minute warning: the details are still captured for
// every failure, but only one record is emitted.
func TestCompressedRolloutReadDiagnosticRateLimitLikeRust(t *testing.T) {
	resetCompressionReadDiagnosticsForTest()
	t.Cleanup(resetCompressionReadDiagnosticsForTest)

	var logs bytes.Buffer
	previousLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelWarn})))
	t.Cleanup(func() { slog.SetDefault(previousLogger) })

	contents := []byte("valid utf8\n")
	complete := singleSegmentZstd(t, contents)
	dir := t.TempDir()
	for i := 0; i < 2; i++ {
		path := filepath.Join(dir, fmt.Sprintf("rollout-%d.jsonl.zst", i))
		damaged := complete[:len(complete)-1]
		if err := os.WriteFile(path, damaged, 0o644); err != nil {
			t.Fatalf("os.WriteFile() error = %v", err)
		}
		if _, readErr, failure := readDiagnosticRolloutFile(t, path, 0); !errors.Is(readErr, io.ErrUnexpectedEOF) || failure == nil {
			t.Fatalf("decode %d: error = %v, failure = %v", i, readErr, failure)
		}
	}

	output := logs.String()
	if got := strings.Count(output, "compressed rollout read failed"); got != 1 {
		t.Fatalf("warning count = %d, want 1; logs = %s", got, output)
	}
	if strings.Contains(output, dir) || strings.Contains(output, "rollout-0.jsonl.zst") || strings.Contains(output, "valid utf8") {
		t.Fatalf("diagnostic log exposes a path or contents: %s", output)
	}
	for _, field := range []string{"error_kind=unexpected_eof", "compressed_bytes=", "first_frame_content_bytes=", "decoded_bytes=", "file_changed="} {
		if !strings.Contains(output, field) {
			t.Fatalf("diagnostic log is missing %q: %s", field, output)
		}
	}
}

func TestFirstFrameContentSizeLikeRust(t *testing.T) {
	magic := []byte{0x28, 0xb5, 0x2f, 0xfd}
	header := func(descriptor byte, fields ...byte) []byte {
		out := append(append([]byte(nil), magic...), descriptor)
		return append(out, fields...)
	}
	cases := []struct {
		name     string
		header   []byte
		size     uint64
		declared bool
		ok       bool
	}{
		{
			name:     "single segment one-byte size",
			header:   header(0x20, 0x0d),
			size:     13,
			declared: true,
			ok:       true,
		},
		{
			name:     "two-byte size adds 256",
			header:   header(0x40, 0x00, 0x00, 0x01),
			size:     512,
			declared: true,
			ok:       true,
		},
		{
			name:     "four-byte size",
			header:   header(0x80, 0x00, 0xe8, 0x03, 0x00, 0x00),
			size:     1000,
			declared: true,
			ok:       true,
		},
		{
			name:     "eight-byte size",
			header:   header(0xc0, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x10, 0x00),
			size:     1 << 52,
			declared: true,
			ok:       true,
		},
		{
			name:     "no size field",
			header:   header(0x00, 0x00),
			declared: false,
			ok:       true,
		},
		{
			name:   "incomplete header",
			header: magic,
			ok:     false,
		},
		{
			name:   "bad magic",
			header: []byte{0x00, 0xb5, 0x2f, 0xfd, 0x20, 0x0d},
			ok:     false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			size, declared, ok := firstFrameContentSize(tc.header)
			if size != tc.size || declared != tc.declared || ok != tc.ok {
				t.Fatalf("firstFrameContentSize() = (%d, %v, %v), want (%d, %v, %v)",
					size, declared, ok, tc.size, tc.declared, tc.ok)
			}
		})
	}
}
