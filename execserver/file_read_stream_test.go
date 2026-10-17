package execserver

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// Rust's `LocalFileSystem::read_file_stream` yields bounded blocks until the end
// of the file, so a copy streams a large file without loading it into memory
// (codex-rs/exec-server/src/local_file_system.rs:170, upstream #54674
// `e7de1f4440`).
func TestOpenLocalFileReadStreamYieldsBoundedBlocksLikeRust(t *testing.T) {
	path := filepath.Join(t.TempDir(), "payload.bin")
	payload := append(bytes.Repeat([]byte{0x5A}, 2*fileReadChunkSize), []byte("tail")...)
	if err := os.WriteFile(path, payload, 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	stream, err := OpenLocalFileReadStream(path)
	if err != nil {
		t.Fatalf("OpenLocalFileReadStream() error = %v", err)
	}
	defer func() { _ = stream.Close() }()

	var collected []byte
	blocks := 0
	for {
		block, ok, err := stream.Next(context.Background())
		if err != nil {
			t.Fatalf("Next() error = %v", err)
		}
		if !ok {
			break
		}
		if len(block) < 1 || len(block) > FileReadChunkSize {
			t.Fatalf("block length = %d, want 1..%d", len(block), FileReadChunkSize)
		}
		blocks++
		collected = append(collected, block...)
	}
	if blocks != 3 {
		t.Fatalf("blocks = %d, want 3", blocks)
	}
	if !bytes.Equal(collected, payload) {
		t.Fatalf("streamed %d bytes, want %d identical bytes", len(collected), len(payload))
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("second Close() error = %v", err)
	}
}

func TestOpenLocalFileReadStreamRejectsNonRegularFileLikeRust(t *testing.T) {
	stream, err := OpenLocalFileReadStream(t.TempDir())
	if err == nil {
		_ = stream.Close()
		t.Fatal("OpenLocalFileReadStream(directory) = nil error, want a rejection")
	}
	if runtime.GOOS != "windows" && !errors.Is(err, os.ErrInvalid) {
		t.Fatalf("directory error = %v, want the regular-file rejection", err)
	}
	_, err = OpenLocalFileReadStream(filepath.Join(t.TempDir(), "missing.bin"))
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing file error = %v, want not-exist", err)
	}
}

func TestOpenLocalFileReadStreamHonorsCancellationLikeRust(t *testing.T) {
	path := filepath.Join(t.TempDir(), "payload.bin")
	if err := os.WriteFile(path, []byte("payload"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	stream, err := OpenLocalFileReadStream(path)
	if err != nil {
		t.Fatalf("OpenLocalFileReadStream() error = %v", err)
	}
	defer func() { _ = stream.Close() }()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := stream.Next(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Next(cancelled) error = %v, want context.Canceled", err)
	}
}
