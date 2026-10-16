package tool

import (
	"bytes"
	"context"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"codex_go/execserver"
)

// streamBlocks builds a block source over the given blocks and reports the
// 1-based pull index before it yields the block, so a test can observe how far
// the writer had progressed when it asked for more input.
func streamBlocks(blocks [][]byte, observe func(pull int)) execserver.FileWriteChunkSource {
	pull := 0
	return func(context.Context) ([]byte, bool, error) {
		if pull >= len(blocks) {
			return nil, false, nil
		}
		if observe != nil {
			observe(pull + 1)
		}
		block := blocks[pull]
		pull++
		return block, true, nil
	}
}

// Rust #54505 `a45b8ccb01`:
// `LocalFileSystem::write_file_stream`
// (codex-rs/exec-server/src/local_file_system.rs:331) replaces the target one
// block at a time, rejects an oversized or empty block, and only reports the
// bytes once every block reached the file. The Go in-process filesystem must
// behave the same, including creating missing parents like its `WriteFile`
// counterpart (tool/environment_filesystem.go:167).
func TestEnvironmentFileSystemWriteFileStreamLocalLikeRust(t *testing.T) {
	root := t.TempDir()
	provider := NewUnifiedExecEnvironmentFileSystems(nil, root)
	local, ok := provider.FileSystemFor("")
	if !ok {
		t.Fatal(`FileSystemFor("") = not usable, want the implicit local environment`)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// A nested target proves the streaming path creates parents like WriteFile.
	relative := filepath.Join("nested", "deep", "streamed.bin")
	target := filepath.Join(root, relative)
	blocks := [][]byte{
		bytes.Repeat([]byte{0xAB}, execserver.FileWriteChunkSize),
		bytes.Repeat([]byte{0xCD}, execserver.FileWriteChunkSize),
		[]byte("tail"),
	}
	var observed []int64
	source := streamBlocks(blocks, func(pull int) {
		info, err := os.Stat(target)
		if err != nil {
			t.Fatalf("stat streamed target: %v", err)
		}
		observed = append(observed, info.Size())
	})

	written, err := local.WriteFileStream(ctx, relative, source, nil)
	if err != nil {
		t.Fatalf("local WriteFileStream() error = %v", err)
	}
	wantWritten := int64(2*execserver.FileWriteChunkSize + 4)
	if written != wantWritten {
		t.Fatalf("local WriteFileStream() written = %d, want %d", written, wantWritten)
	}
	// The replacement open truncates the target before the first pull, so every
	// pull sees exactly the blocks already written and never a partial block
	// (Rust `streaming_replacement_writes_before_reading_the_next_block`).
	chunk := int64(execserver.FileWriteChunkSize)
	wantObserved := []int64{0, chunk, 2 * chunk}
	if len(observed) != len(wantObserved) {
		t.Fatalf("local WriteFileStream() pulled %d blocks, want %d", len(observed), len(wantObserved))
	}
	for i := range wantObserved {
		if observed[i] != wantObserved[i] {
			t.Fatalf("target size before pull %d = %d, want %d (a block must be finished before the next is read)", i+1, observed[i], wantObserved[i])
		}
	}
	contents, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read streamed target: %v", err)
	}
	var want []byte
	for _, block := range blocks {
		want = append(want, block...)
	}
	if !bytes.Equal(contents, want) {
		t.Fatalf("streamed target length = %d, want %d", len(contents), len(want))
	}

	// An empty stream still truncates the target, like Rust's
	// `streaming_replacement_copies_binary_and_empty_files`.
	if err := os.WriteFile(target, bytes.Repeat([]byte{0x2A}, 64), 0o600); err != nil {
		t.Fatalf("seed target: %v", err)
	}
	written, err = local.WriteFileStream(ctx, relative, streamBlocks(nil, nil), nil)
	if err != nil {
		t.Fatalf("local WriteFileStream(empty) error = %v", err)
	}
	if written != 0 {
		t.Fatalf("local WriteFileStream(empty) written = %d, want 0", written)
	}
	info, err := os.Stat(target)
	if err != nil {
		t.Fatalf("stat truncated target: %v", err)
	}
	if info.Size() != 0 {
		t.Fatalf("target size after empty stream = %d, want 0", info.Size())
	}
}

// Rust #54505: a block must be nonempty and no larger than
// `FILE_WRITE_CHUNK_SIZE`, so the in-process filesystem rejects a bad stream
// instead of writing it (codex-rs/exec-server/src/local_file_system.rs:342).
func TestEnvironmentFileSystemWriteFileStreamLocalRejectsInvalidBlocksLikeRust(t *testing.T) {
	root := t.TempDir()
	local, ok := NewUnifiedExecEnvironmentFileSystems(nil, root).FileSystemFor("")
	if !ok {
		t.Fatal(`FileSystemFor("") = not usable, want the implicit local environment`)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cases := map[string][][]byte{
		"empty_block":     {[]byte("ok"), {}},
		"oversized_block": {bytes.Repeat([]byte{0x01}, execserver.FileWriteChunkSize+1)},
	}
	for name, blocks := range cases {
		t.Run(name, func(t *testing.T) {
			relative := name + ".bin"
			written, err := local.WriteFileStream(ctx, relative, streamBlocks(blocks, nil), nil)
			if err == nil || !strings.Contains(err.Error(), "invalid file stream block length") {
				t.Fatalf("local WriteFileStream() error = %v, want the invalid block length error", err)
			}
			if written != 0 {
				t.Fatalf("local WriteFileStream() written = %d, want 0", written)
			}
		})
	}

	written, err := local.WriteFileStream(ctx, "no-source.bin", nil, nil)
	if err == nil || !strings.Contains(err.Error(), "file write stream source is required") {
		t.Fatalf("local WriteFileStream(nil source) error = %v, want the missing-source error", err)
	}
	if written != 0 {
		t.Fatalf("local WriteFileStream(nil source) written = %d, want 0", written)
	}
}

// Rust #54505 (`RemoteFileSystem::write_file_stream`,
// codex-rs/exec-server/src/remote_file_system.rs:412): against a real executor
// that advertises `file_write_streaming`, the remote environment filesystem
// streams a replacement and reports the bytes written; the same call must also
// truncate the target for an empty stream.
func TestEnvironmentFileSystemWriteFileStreamRemoteLikeRust(t *testing.T) {
	serverURL, stop := startEnvironmentFileSystemExecServer(t)
	defer stop()
	remoteDir := t.TempDir()
	provider := NewUnifiedExecEnvironmentFileSystems([]UnifiedExecEnvironment{{
		ID:            "remote-x",
		CWD:           remoteDir,
		ExecServerURL: serverURL,
	}}, ".")
	remote, ok := provider.FileSystemFor("remote-x")
	if !ok {
		t.Fatalf(`FileSystemFor("remote-x") = not usable, want the selected environment`)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// The real in-process executor advertises the capability
	// (execserver/server.go:3549), which is what selects the streaming path
	// over the legacy buffered write.
	probe, err := execserver.DialClient(ctx, serverURL, "streaming-capability-probe")
	if err != nil {
		t.Fatalf("DialClient() error = %v", err)
	}
	streaming, err := probe.SupportsFileWriteStreaming(ctx)
	probe.Close()
	if err != nil {
		t.Fatalf("SupportsFileWriteStreaming() error = %v", err)
	}
	if !streaming {
		t.Fatal("executor advertises file_write_streaming = false, want true so the remote stream path is exercised")
	}

	const name = "remote-streamed.bin"
	target := filepath.Join(remoteDir, name)
	if err := os.WriteFile(target, bytes.Repeat([]byte{0x2A}, 100), 0o600); err != nil {
		t.Fatalf("seed remote target: %v", err)
	}
	blocks := [][]byte{{0, 255, 13, 10}, bytes.Repeat([]byte{0x5A}, 70000)}
	var want []byte
	for _, block := range blocks {
		want = append(want, block...)
	}
	written, err := remote.WriteFileStream(ctx, name, streamBlocks(blocks, nil), nil)
	if err != nil {
		t.Fatalf("remote WriteFileStream() error = %v", err)
	}
	if written != int64(len(want)) {
		t.Fatalf("remote WriteFileStream() written = %d, want %d", written, len(want))
	}
	got, err := remote.ReadFile(ctx, name, nil)
	if err != nil {
		t.Fatalf("remote ReadFile() error = %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("remote target length = %d, want %d bytes written by the stream", len(got), len(want))
	}
	metadata, err := remote.GetMetadata(ctx, name, nil)
	if err != nil {
		t.Fatalf("remote GetMetadata() error = %v", err)
	}
	if !metadata.IsFile || metadata.Size != int64(len(want)) {
		t.Fatalf("remote GetMetadata() = %#v, want a file of %d bytes", metadata, len(want))
	}

	// An empty stream truncates the target through the same open/close pair.
	written, err = remote.WriteFileStream(ctx, name, streamBlocks(nil, nil), nil)
	if err != nil {
		t.Fatalf("remote WriteFileStream(empty) error = %v", err)
	}
	if written != 0 {
		t.Fatalf("remote WriteFileStream(empty) written = %d, want 0", written)
	}
	got, err = remote.ReadFile(ctx, name, nil)
	if err != nil {
		t.Fatalf("remote ReadFile() after empty stream error = %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("remote target length after empty stream = %d, want 0", len(got))
	}
}

// Rust #54505: the exec-server wire block is base64 like every other chunk
// (codex-rs/exec-server/src/protocol.rs, `FsWriteBlockParams`). A remote block is
// therefore never sent as raw bytes, which the local/base64 round trip must
// preserve for binary input.
func TestEnvironmentFileSystemWriteFileStreamRemoteRoundTripsBinaryLikeRust(t *testing.T) {
	serverURL, stop := startEnvironmentFileSystemExecServer(t)
	defer stop()
	remoteDir := t.TempDir()
	provider := NewUnifiedExecEnvironmentFileSystems([]UnifiedExecEnvironment{{
		ID:            "remote-x",
		CWD:           remoteDir,
		ExecServerURL: serverURL,
	}}, ".")
	remote, ok := provider.FileSystemFor("remote-x")
	if !ok {
		t.Fatal(`FileSystemFor("remote-x") = not usable, want the selected environment`)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	payload := make([]byte, 4096)
	for i := range payload {
		payload[i] = byte(i)
	}
	encoded := base64.StdEncoding.EncodeToString(payload)
	if err := os.WriteFile(filepath.Join(remoteDir, "binary.bin"), []byte(encoded), 0o600); err != nil {
		t.Fatalf("seed binary target: %v", err)
	}
	written, err := remote.WriteFileStream(ctx, "binary.bin", streamBlocks([][]byte{payload}, nil), nil)
	if err != nil {
		t.Fatalf("remote WriteFileStream() error = %v", err)
	}
	if written != int64(len(payload)) {
		t.Fatalf("remote WriteFileStream() written = %d, want %d", written, len(payload))
	}
	got, err := remote.ReadFile(ctx, "binary.bin", nil)
	if err != nil {
		t.Fatalf("remote ReadFile() error = %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("remote binary target differs from the streamed payload (%d vs %d bytes)", len(got), len(payload))
	}
}
