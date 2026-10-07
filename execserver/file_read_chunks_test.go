package execserver

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
)

// Rust #49696 adds regression coverage for 4 MiB reads with both
// symlink-following options; the 512 MiB limit, the symlink options and the
// regular-file validation stay unchanged (local_file_system_read_tests.rs).
func TestReadFileContextReadsFourMiBLikeRust(t *testing.T) {
	payload := bytes.Repeat([]byte("x"), 4*1024*1024)
	path := filepath.Join(t.TempDir(), "payload.bin")
	if err := os.WriteFile(path, payload, 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	follow := true
	noFollow := false
	options := []struct {
		name           string
		followSymlinks *bool
	}{
		{"unset", nil},
		{"follow", &follow},
		{"no_follow", &noFollow},
	}
	for _, option := range options {
		t.Run(option.name, func(t *testing.T) {
			response, err := readFileContext(context.Background(), &FSReadFileParams{Path: path, FollowSymlinks: option.followSymlinks})
			if err != nil {
				t.Fatalf("readFileContext() error = %v", err)
			}
			data, err := base64.StdEncoding.DecodeString(response.DataBase64)
			if err != nil {
				t.Fatalf("DecodeString() error = %v", err)
			}
			if !bytes.Equal(data, payload) {
				t.Fatalf("read %d bytes, want %d", len(data), len(payload))
			}
		})
	}
}

// cancellingChunkReader cancels its context after returning the first chunk, so
// the next chunk boundary is where cancellation must be observed.
type cancellingChunkReader struct {
	cancel context.CancelFunc
	chunk  int
	reads  int
}

func (r *cancellingChunkReader) Read(p []byte) (int, error) {
	r.reads++
	if r.reads > 1 {
		return 0, io.EOF
	}
	count := len(p)
	if count > r.chunk {
		count = r.chunk
	}
	for i := 0; i < count; i++ {
		p[i] = 'y'
	}
	r.cancel()
	return count, nil
}

// Rust #49696: dropping the async caller cancels further reads between chunks.
// The reader must not be asked for a second chunk after cancellation.
func TestReadBoundedFileDataCancelsBetweenChunksLikeRust(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	reader := &cancellingChunkReader{cancel: cancel, chunk: fileReadChunkSize}
	if _, err := readBoundedFileData(ctx, reader); !errors.Is(err, context.Canceled) {
		t.Fatalf("readBoundedFileData() error = %v, want context.Canceled", err)
	}
	if reader.reads != 1 {
		t.Fatalf("reader was read %d times, want exactly 1 chunk", reader.reads)
	}
}

func TestReadBoundedFileDataStopsBeforeReadingCancelledRequestLikeRust(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	reader := &cancellingChunkReader{cancel: func() {}, chunk: fileReadChunkSize}
	if _, err := readBoundedFileData(ctx, reader); !errors.Is(err, context.Canceled) {
		t.Fatalf("readBoundedFileData() error = %v, want context.Canceled", err)
	}
	if reader.reads != 0 {
		t.Fatalf("reader was read %d times for a cancelled request, want 0", reader.reads)
	}
}

// The request context reaches the file read path through readFileContext, which
// is what the fs/readFile handler now passes down.
func TestReadFileContextHonorsCancellationLikeRust(t *testing.T) {
	path := filepath.Join(t.TempDir(), "payload.bin")
	if err := os.WriteFile(path, bytes.Repeat([]byte("z"), 1024), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := readFileContext(ctx, &FSReadFileParams{Path: path}); !errors.Is(err, context.Canceled) {
		t.Fatalf("readFileContext() error = %v, want context.Canceled", err)
	}
}
