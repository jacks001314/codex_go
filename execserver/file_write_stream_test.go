package execserver

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// fileWriteStreamExecutor is a scripted exec server that records every
// replacement request a client sends, so a test can prove which wire path a
// streaming write took. It is the Go counterpart of the fixture the upstream
// streaming tests drive (Rust #54505 `a45b8ccb01`,
// codex-rs/exec-server/tests/file_stream.rs).
type fileWriteStreamExecutor struct {
	mu sync.Mutex
	// methods is the ordered list of request methods that reached the executor.
	methods []string
	// openModes is the `mode` of every fs/open, in order.
	openModes []string
	// blocks is every fs/writeBlock the executor received, in order.
	blocks []FSWriteBlockParams
	// writeFiles is every legacy whole-file fs/writeFile, in order.
	writeFiles []FSWriteFileParams
	// closes counts the fs/close calls, including the best-effort close a
	// failed transfer is still expected to send.
	closes int
	// failBlockAt fails the 1-based Nth fs/writeBlock with a server error.
	failBlockAt int
}

func (e *fileWriteStreamExecutor) snapshot() ([]string, []string, []FSWriteBlockParams, []FSWriteFileParams, int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]string(nil), e.methods...), append([]string(nil), e.openModes...),
		append([]FSWriteBlockParams(nil), e.blocks...), append([]FSWriteFileParams(nil), e.writeFiles...), e.closes
}

// blockWrites reports how many fs/writeBlock requests the executor has already
// handled, which is how a source can prove the previous block was finished
// before it was asked for the next one.
func (e *fileWriteStreamExecutor) blockWrites() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.blocks)
}

// startFileWriteStreamExecutor runs a scripted exec server that advertises (or
// does not advertise) `file_write_streaming` and returns a dialed client.
func startFileWriteStreamExecutor(t *testing.T, streaming bool) (*Client, *fileWriteStreamExecutor) {
	t.Helper()
	return startFileWriteStreamExecutorWith(t, streaming, &fileWriteStreamExecutor{})
}

// startWriteBlockFailureExecutor arms a scripted executor so its second
// fs/writeBlock fails with a server error.
func startWriteBlockFailureExecutor(t *testing.T) (*Client, *fileWriteStreamExecutor) {
	t.Helper()
	return startFileWriteStreamExecutorWith(t, true, &fileWriteStreamExecutor{failBlockAt: 2})
}

// blockSource returns a FileWriteChunkSource over the given blocks, calling
// observe before each pull with the 1-based pull index.
func blockSource(blocks [][]byte, observe func(pull int)) FileWriteChunkSource {
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

func decodeBlocks(t *testing.T, blocks []FSWriteBlockParams) [][]byte {
	t.Helper()
	out := make([][]byte, 0, len(blocks))
	for i, block := range blocks {
		decoded, err := base64.StdEncoding.DecodeString(block.Chunk)
		if err != nil {
			t.Fatalf("fs/writeBlock %d chunk is not base64: %v", i, err)
		}
		out = append(out, decoded)
	}
	return out
}

// Rust #54505 `a45b8ccb01`: `RemoteFileSystem::write_file_stream` drives a
// streaming executor with `fs/open`(replace) + `fs/writeBlock` per block +
// `fs/close` and reports the bytes written
// (codex-rs/exec-server/src/remote_file_system.rs:412).
func TestWriteFileStreamStreamsReplacementLikeRust(t *testing.T) {
	client, exec := startFileWriteStreamExecutor(t, true)

	chunk := FileWriteChunkSize
	blocks := [][]byte{bytes.Repeat([]byte{0xAB}, chunk), bytes.Repeat([]byte{0xCD}, chunk), []byte("tail")}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	written, err := client.WriteFileStream(ctx, "file:///tmp/stream.bin", nil, blockSource(blocks, nil))
	if err != nil {
		t.Fatalf("WriteFileStream() error = %v", err)
	}
	if want := int64(2*chunk + 4); written != want {
		t.Fatalf("WriteFileStream() written = %d, want %d", written, want)
	}

	methods, openModes, sent, writeFiles, closes := exec.snapshot()
	wantMethods := []string{
		MethodInitialize, MethodFSOpen, MethodFSWriteBlock, MethodFSWriteBlock, MethodFSWriteBlock, MethodFSClose,
	}
	if strings.Join(methods, ",") != strings.Join(wantMethods, ",") {
		t.Fatalf("executor saw methods %v, want %v", methods, wantMethods)
	}
	if len(openModes) != 1 || openModes[0] != FSOpenModeReplace {
		t.Fatalf("executor saw fs/open modes %v, want exactly [%s]", openModes, FSOpenModeReplace)
	}
	if len(writeFiles) != 0 {
		t.Fatalf("executor saw %d legacy fs/writeFile requests, want 0", len(writeFiles))
	}
	if closes != 1 {
		t.Fatalf("executor saw %d fs/close requests, want 1", closes)
	}
	if len(sent) != len(blocks) {
		t.Fatalf("executor saw %d block writes, want %d", len(sent), len(blocks))
	}
	var offset uint64
	for i, block := range sent {
		if block.Offset != offset {
			t.Fatalf("block %d offset = %d, want %d", i, block.Offset, offset)
		}
		offset += uint64(len(blocks[i]))
	}
	decoded := decodeBlocks(t, sent)
	for i := range blocks {
		if !bytes.Equal(decoded[i], blocks[i]) {
			t.Fatalf("block %d payload length = %d, want %d bytes of the source block", i, len(decoded[i]), len(blocks[i]))
		}
	}
	if sent[0].HandleID == "" || sent[0].HandleID != sent[2].HandleID {
		t.Fatalf("block writes used handle ids %q/%q, want one nonempty replacement handle", sent[0].HandleID, sent[2].HandleID)
	}
}

// Rust #54505 `a45b8ccb01`
// (`streaming_replacement_writes_before_reading_the_next_block`,
// codex-rs/exec-server/tests/file_stream.rs:563): the writer must finish a block
// before asking its source for more data, so a reader can never observe a
// partially written block.
func TestWriteFileStreamWritesEachBlockBeforeReadingTheNextLikeRust(t *testing.T) {
	client, exec := startFileWriteStreamExecutor(t, true)

	blocks := [][]byte{[]byte("first"), []byte("second"), []byte("third")}
	observed := make([]int, 0, len(blocks))
	source := blockSource(blocks, func(pull int) {
		observed = append(observed, exec.blockWrites())
	})

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	written, err := client.WriteFileStream(ctx, "file:///tmp/sequential.bin", nil, source)
	if err != nil {
		t.Fatalf("WriteFileStream() error = %v", err)
	}
	if want := int64(len("firstsecondthird")); written != want {
		t.Fatalf("WriteFileStream() written = %d, want %d", written, want)
	}
	if want := []int{0, 1, 2}; fmt.Sprint(observed) != fmt.Sprint(want) {
		t.Fatalf("block writes completed before each source pull = %v, want %v (the writer must flush a block before reading the next)", observed, want)
	}
}

// Rust #54505 (`RemoteFileSystem::write_file_stream`,
// codex-rs/exec-server/src/remote_file_system.rs:433): an executor without
// `file_write_streaming` buffers the whole input and sends one `fs/writeFile`,
// and the capability is chosen before anything is opened.
func TestWriteFileStreamBuffersLegacyExecutorLikeRust(t *testing.T) {
	client, exec := startFileWriteStreamExecutor(t, false)

	blocks := [][]byte{[]byte("legacy"), []byte("-write"), []byte("-path")}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	written, err := client.WriteFileStream(ctx, "file:///tmp/legacy.bin", nil, blockSource(blocks, nil))
	if err != nil {
		t.Fatalf("WriteFileStream() error = %v", err)
	}
	want := int64(len("legacy-write-path"))
	if written != want {
		t.Fatalf("WriteFileStream() written = %d, want %d", written, want)
	}

	methods, openModes, sent, writeFiles, closes := exec.snapshot()
	if strings.Join(methods, ",") != strings.Join([]string{MethodInitialize, MethodFSWriteFile}, ",") {
		t.Fatalf("executor saw methods %v, want [initialize fs/writeFile] (no replacement open or block write may reach a legacy executor)", methods)
	}
	if len(openModes) != 0 || len(sent) != 0 || closes != 0 {
		t.Fatalf("executor saw fs/open modes %v, blocks %d, closes %d, want none", openModes, len(sent), closes)
	}
	if len(writeFiles) != 1 {
		t.Fatalf("executor saw %d fs/writeFile requests, want 1", len(writeFiles))
	}
	decoded, err := base64.StdEncoding.DecodeString(writeFiles[0].DataBase64)
	if err != nil {
		t.Fatalf("fs/writeFile dataBase64 is not base64: %v", err)
	}
	if got := string(decoded); got != "legacy-write-path" {
		t.Fatalf("fs/writeFile body = %q, want the buffered stream", got)
	}
}

// Rust #54505 (`streaming_replacement_preserves_partial_failure_and_releases_handles`,
// codex-rs/exec-server/tests/file_stream.rs:622): a failed source surfaces its
// error and the remote handle capacity is still released, and a failed block
// write likewise still closes the handle (remote_file_system.rs:490).
func TestWriteFileStreamClosesTheHandleAfterAFailureLikeRust(t *testing.T) {
	t.Run("source_failure", func(t *testing.T) {
		client, exec := startFileWriteStreamExecutor(t, true)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		pull := 0
		source := func(context.Context) ([]byte, bool, error) {
			if pull == 0 {
				pull++
				return []byte("partial"), true, nil
			}
			return nil, false, errors.New("source failed")
		}
		written, err := client.WriteFileStream(ctx, "file:///tmp/partial.bin", nil, source)
		if err == nil || !strings.Contains(err.Error(), "source failed") {
			t.Fatalf("WriteFileStream() error = %v, want the source failure", err)
		}
		if written != 0 {
			t.Fatalf("WriteFileStream() written = %d, want 0 on failure", written)
		}
		_, _, sent, _, closes := exec.snapshot()
		if len(sent) != 1 {
			t.Fatalf("executor saw %d block writes, want the one confirmed block", len(sent))
		}
		if closes < 1 {
			t.Fatalf("executor saw %d fs/close requests, want at least 1 so the abandoned handle is released", closes)
		}
	})

	t.Run("block_write_failure", func(t *testing.T) {
		client, exec := startWriteBlockFailureExecutor(t)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		blocks := [][]byte{[]byte("first"), []byte("second"), []byte("third")}
		written, err := client.WriteFileStream(ctx, "file:///tmp/block-failure.bin", nil, blockSource(blocks, nil))
		if err == nil || !strings.Contains(err.Error(), "scripted block write failure") {
			t.Fatalf("WriteFileStream() error = %v, want the scripted block write failure", err)
		}
		if written != 0 {
			t.Fatalf("WriteFileStream() written = %d, want 0 on failure", written)
		}
		_, _, sent, _, closes := exec.snapshot()
		if len(sent) != 2 {
			t.Fatalf("executor saw %d block writes, want 2 (the failure stops the transfer)", len(sent))
		}
		if closes != 1 {
			t.Fatalf("executor saw %d fs/close requests, want 1 for the abandoned handle", closes)
		}
	})
}

// Rust #54505 (`RemoteFileSystem::write_file_stream`, remote_file_system.rs:421
// and local_file_system.rs:342): a block must be nonempty and no larger than
// `FILE_WRITE_CHUNK_SIZE`; the legacy buffer path rejects an invalid stream
// before it writes anything.
func TestWriteFileStreamRejectsInvalidBlocksLikeRust(t *testing.T) {
	oversized := bytes.Repeat([]byte{0x7F}, FileWriteChunkSize+1)

	t.Run("streaming_empty_block", func(t *testing.T) {
		client, exec := startFileWriteStreamExecutor(t, true)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		written, err := client.WriteFileStream(ctx, "file:///tmp/empty-block.bin", nil, blockSource([][]byte{{}}, nil))
		if err == nil || !strings.Contains(err.Error(), "invalid file stream block length") {
			t.Fatalf("WriteFileStream() error = %v, want the invalid block length error", err)
		}
		if written != 0 {
			t.Fatalf("WriteFileStream() written = %d, want 0", written)
		}
		_, _, sent, _, closes := exec.snapshot()
		if len(sent) != 0 {
			t.Fatalf("executor saw %d block writes, want 0 (the empty block is rejected before the write)", len(sent))
		}
		if closes < 1 {
			t.Fatalf("executor saw %d fs/close requests, want at least 1 for the already-opened handle", closes)
		}
	})

	t.Run("streaming_oversized_block", func(t *testing.T) {
		client, _ := startFileWriteStreamExecutor(t, true)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		written, err := client.WriteFileStream(ctx, "file:///tmp/oversized-block.bin", nil, blockSource([][]byte{oversized}, nil))
		if err == nil || !strings.Contains(err.Error(), "invalid file stream block length") {
			t.Fatalf("WriteFileStream() error = %v, want the invalid block length error", err)
		}
		if written != 0 {
			t.Fatalf("WriteFileStream() written = %d, want 0", written)
		}
	})

	t.Run("legacy_invalid_block_writes_nothing", func(t *testing.T) {
		client, exec := startFileWriteStreamExecutor(t, false)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		written, err := client.WriteFileStream(ctx, "file:///tmp/legacy-invalid.bin", nil, blockSource([][]byte{[]byte("ok"), {}}, nil))
		if err == nil || !strings.Contains(err.Error(), "invalid file stream block length") {
			t.Fatalf("WriteFileStream() error = %v, want the invalid block length error", err)
		}
		if written != 0 {
			t.Fatalf("WriteFileStream() written = %d, want 0", written)
		}
		methods, _, _, writeFiles, _ := exec.snapshot()
		if len(writeFiles) != 0 || strings.Join(methods, ",") != MethodInitialize {
			t.Fatalf("executor saw methods %v, want only the initialize handshake: an invalid stream must never reach a legacy executor", methods)
		}
	})
}

// Rust #54505: the bounded block contract itself
// (codex-rs/file-system/src/lib.rs:770, "Input chunks must be nonempty and no
// larger than FILE_WRITE_CHUNK_SIZE").
func TestValidateFileWriteBlockLikeRust(t *testing.T) {
	if err := ValidateFileWriteBlock(nil); err == nil {
		t.Fatal("ValidateFileWriteBlock(nil) = nil, want an error for an empty block")
	}
	if err := ValidateFileWriteBlock([]byte{}); err == nil {
		t.Fatal("ValidateFileWriteBlock(empty) = nil, want an error for an empty block")
	}
	if err := ValidateFileWriteBlock([]byte{0x01}); err != nil {
		t.Fatalf("ValidateFileWriteBlock(1 byte) error = %v", err)
	}
	if err := ValidateFileWriteBlock(make([]byte, FileWriteChunkSize)); err != nil {
		t.Fatalf("ValidateFileWriteBlock(FileWriteChunkSize) error = %v", err)
	}
	if err := ValidateFileWriteBlock(make([]byte, FileWriteChunkSize+1)); err == nil {
		t.Fatal("ValidateFileWriteBlock(FileWriteChunkSize+1) = nil, want an error for an oversized block")
	}
	if FileWriteChunkSize != 1024*1024 {
		t.Fatalf("FileWriteChunkSize = %d, want the Rust FILE_WRITE_CHUNK_SIZE of 1048576", FileWriteChunkSize)
	}
}

// Rust #54505: a client with no source, and a nil client, must fail loudly
// rather than silently creating an empty target.
func TestWriteFileStreamValidatesArgumentsLikeRust(t *testing.T) {
	var nilClient *Client
	if _, err := nilClient.WriteFileStream(context.Background(), "file:///tmp/nil.bin", nil, blockSource(nil, nil)); err == nil {
		t.Fatal("nil client WriteFileStream() = nil, want an error")
	}

	client, exec := startFileWriteStreamExecutor(t, true)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := client.WriteFileStream(ctx, "file:///tmp/nil-source.bin", nil, nil); err == nil ||
		!strings.Contains(err.Error(), "file write stream source is required") {
		t.Fatalf("WriteFileStream(nil source) error = %v, want the missing-source error", err)
	}
	methods, _, _, _, _ := exec.snapshot()
	if strings.Join(methods, ",") != MethodInitialize {
		t.Fatalf("executor saw methods %v, want only the initialize handshake", methods)
	}
}

// startFileWriteStreamExecutorWith starts a scripted executor that uses a
// caller-supplied probe, which lets a test pre-arm an injected failure.
func startFileWriteStreamExecutorWith(t *testing.T, streaming bool, exec *fileWriteStreamExecutor) (*Client, *fileWriteStreamExecutor) {
	t.Helper()
	serverErr := make(chan error, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			serverErr <- err
			return
		}
		defer conn.CloseNow()
		// The real transport raises the read limit for block writes
		// (execserver/transport.go:143, 16MiB), so the scripted server must too.
		conn.SetReadLimit(16 * 1024 * 1024)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		for {
			_, payload, err := conn.Read(ctx)
			if err != nil {
				return
			}
			var request execServerWireRequest
			if err := json.Unmarshal(payload, &request); err != nil {
				serverErr <- err
				return
			}
			if request.Method == "" || len(request.ID) == 0 {
				continue
			}
			var (
				result any
				rpcErr any
			)
			exec.mu.Lock()
			exec.methods = append(exec.methods, request.Method)
			switch request.Method {
			case MethodInitialize:
				exec.mu.Unlock()
				info := EnvironmentInfo{
					Shell:        ShellInfo{Name: "bash", Path: "/bin/bash"},
					Capabilities: EnvironmentCapabilities{FileWriteStreaming: streaming},
				}
				result = InitializeResponse{SessionID: "file-write-stream-failure-session", EnvironmentInfo: &info}
			case MethodFSOpen:
				var params FSOpenParams
				if err := json.Unmarshal(request.Params, &params); err != nil {
					exec.mu.Unlock()
					serverErr <- err
					return
				}
				exec.openModes = append(exec.openModes, params.Mode)
				exec.mu.Unlock()
				result = FSOpenResponse{HandleID: params.HandleID}
			case MethodFSWriteBlock:
				var params FSWriteBlockParams
				if err := json.Unmarshal(request.Params, &params); err != nil {
					exec.mu.Unlock()
					serverErr <- err
					return
				}
				exec.blocks = append(exec.blocks, params)
				fail := exec.failBlockAt != 0 && len(exec.blocks) == exec.failBlockAt
				exec.mu.Unlock()
				if fail {
					rpcErr = map[string]any{"code": -32000, "message": "scripted block write failure"}
				} else {
					result = FSWriteBlockResponse{}
				}
			case MethodFSClose:
				exec.closes++
				exec.mu.Unlock()
				result = FSCloseResponse{}
			case MethodFSWriteFile:
				var params FSWriteFileParams
				if err := json.Unmarshal(request.Params, &params); err != nil {
					exec.mu.Unlock()
					serverErr <- err
					return
				}
				exec.writeFiles = append(exec.writeFiles, params)
				exec.mu.Unlock()
				result = FSWriteFileResponse{}
			default:
				exec.mu.Unlock()
				result = map[string]any{}
			}
			encoded, err := json.Marshal(map[string]any{
				"jsonrpc": "2.0",
				"id":      request.ID,
				"result":  result,
				"error":   rpcErr,
			})
			if err != nil {
				serverErr <- err
				return
			}
			if err := conn.Write(ctx, websocket.MessageText, encoded); err != nil {
				return
			}
		}
	}))
	t.Cleanup(func() {
		server.Close()
		select {
		case err := <-serverErr:
			t.Errorf("scripted executor error = %v", err)
		default:
		}
	})

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	client, err := DialClient(ctx, "ws"+strings.TrimPrefix(server.URL, "http"), "file-write-stream-failure-test")
	if err != nil {
		t.Fatalf("DialClient() error = %v", err)
	}
	t.Cleanup(func() { client.Close() })
	return client, exec
}
