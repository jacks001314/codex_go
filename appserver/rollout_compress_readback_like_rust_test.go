package appserver

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"

	"codex_go/rollout"
	"codex_go/session"
	"codex_go/turn"
)

// Mirrors Rust #46020's lossless readback assertion: after `rollout/compress`
// replaces a cold rollout with its `.jsonl.zst` representation, decoding the
// compressed file reproduces the original bytes exactly and the plain path still
// resolves to that representation.
func TestRuntimeRouterRolloutCompressReadsBackLosslesslyLikeRust(t *testing.T) {
	home := t.TempDir()
	sessions := filepath.Join(home, rollout.SessionsSubdir)
	plainPath := filepath.Join(sessions, "rollout-2025-01-05T12-00-00-compress-readback.jsonl")
	writeRolloutCompressTestRollout(t, plainPath, "compress-readback")
	original, err := os.ReadFile(plainPath)
	if err != nil {
		t.Fatalf("ReadFile(rollout): %v", err)
	}
	cold := time.Now().Add(-8 * 24 * time.Hour)
	if err := os.Chtimes(plainPath, cold, cold); err != nil {
		t.Fatalf("Chtimes: %v", err)
	}

	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(session.NewStore(sessions)),
		Turns:        turn.NewTurnService(),
		Agent:        newRecordingRuntimeAgent("ok"),
		ThreadStatus: NewThreadStatusManager(),
	})
	response := router.Handle(&Request{JSONRPC: "2.0", ID: IntID(1), Method: MethodRolloutCompress})
	if response.Error != nil {
		t.Fatalf("rollout/compress error: %+v", response.Error)
	}
	if _, ok := response.Result.(*RolloutCompressResponse); !ok {
		t.Fatalf("rollout/compress result = %#v, want RolloutCompressResponse", response.Result)
	}

	compressedPath := plainPath + ".zst"
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if _, statErr := os.Stat(compressedPath); statErr == nil {
			if _, plainErr := os.Stat(plainPath); os.IsNotExist(plainErr) {
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	if _, err := os.Stat(compressedPath); err != nil {
		t.Fatalf("compressed rollout missing: %v", err)
	}

	file, err := os.Open(compressedPath)
	if err != nil {
		t.Fatalf("Open(compressed): %v", err)
	}
	defer func() { _ = file.Close() }()
	decoder, err := zstd.NewReader(file)
	if err != nil {
		t.Fatalf("zstd.NewReader: %v", err)
	}
	defer decoder.Close()
	decoded, err := io.ReadAll(decoder)
	if err != nil {
		t.Fatalf("ReadAll(decoded): %v", err)
	}
	if !bytes.Equal(decoded, original) {
		t.Fatalf("compressed rollout is not lossless:\n got %q\nwant %q", decoded, original)
	}

	lines, _, err := rollout.Load(plainPath)
	if err != nil {
		t.Fatalf("rollout.Load(plain path) error = %v", err)
	}
	if len(lines) == 0 {
		t.Fatal("plain path did not resolve to the compressed rollout")
	}
}

// Mirrors Rust's rollout_compress_rejects_non_local_thread_stores: Go's thread
// router always owns a filesystem store, so the guard fires when the store root
// carries no local codex home instead of silently scheduling a pass with nowhere
// to run.
func TestRuntimeRouterRolloutCompressRejectsMissingLocalCodexHomeLikeRust(t *testing.T) {
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(session.NewStore("")),
		Turns:        turn.NewTurnService(),
		Agent:        newRecordingRuntimeAgent("ok"),
		ThreadStatus: NewThreadStatusManager(),
	})
	response := router.Handle(&Request{JSONRPC: "2.0", ID: IntID(1), Method: MethodRolloutCompress})
	if response.Error == nil {
		t.Fatal("rollout/compress accepted a store without a local codex home")
	}
	if !strings.Contains(response.Error.Message, "requires a local codex home") {
		t.Fatalf("rollout/compress error = %q, want the local store requirement", response.Error.Message)
	}
}
