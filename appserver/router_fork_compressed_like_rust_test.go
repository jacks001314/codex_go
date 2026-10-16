package appserver

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"codex_go/session"
)

// Mirrors Rust #54008 for the fork entry point: `thread_fork_inner`
// (app-server/src/request_processors/thread_processor.rs:5155-5161) loads its
// source thread through `read_stored_thread_for_resume`, which resolves the
// requested rollout path with `resolve_requested_rollout_path`
// (thread-store/src/local/read_thread.rs:202-263). Go derives the source thread
// id from that same path in `threadForkSourceID`, so it must resolve the
// compressed sibling too: otherwise a `thread/fork` requested through the logical
// `.jsonl` path a client obtained from thread/list dies on a bare `os.Stat` even
// though the compressed rollout is present and loadable.
func TestRouterForkCompressedRolloutByLogicalPathLikeRust(t *testing.T) {
	store := session.NewStore(t.TempDir())
	router := NewRouter(store)
	router.SetClock(func() time.Time { return fixedTime() })
	logicalPath := resumeRolloutFixture(t, t.TempDir(), "compressed-fork-thread")
	compressResumeRolloutLikeStartupCompression(t, logicalPath)
	if _, err := os.Stat(logicalPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("plain rollout still present after compression: err = %v", err)
	}
	if _, err := os.Stat(logicalPath + ".zst"); err != nil {
		t.Fatalf("compressed rollout missing: %v", err)
	}

	response := router.Handle(requestWithParams(t, IntID(1), MethodThreadFork, ThreadForkParams{
		ThreadID: "not-the-rollout-thread",
		Path:     &logicalPath,
	}))
	if response.Error != nil {
		t.Fatalf("fork by logical path error = %+v", response.Error)
	}
	fork := response.Result.(*ThreadForkResponse)
	if fork.Thread == nil {
		t.Fatalf("fork thread = %+v", fork.Thread)
	}
	if fork.Thread.ID == "compressed-fork-thread" {
		t.Fatalf("fork reused the source thread id %q", fork.Thread.ID)
	}
	if fork.Thread.ForkedFromID == nil || *fork.Thread.ForkedFromID != "compressed-fork-thread" {
		t.Fatalf("fork thread ForkedFromID = %+v, want the compressed source thread", fork.Thread.ForkedFromID)
	}
	if len(fork.Thread.Turns) != 1 {
		t.Fatalf("fork turns = %+v, want the compressed rollout's single turn", fork.Thread.Turns)
	}
	if len(fork.Thread.Turns[0].Items) != 1 || fork.Thread.Turns[0].Items[0].Text != "compressed history" {
		t.Fatalf("fork turn items = %+v", fork.Thread.Turns[0].Items)
	}

	// The source id must come from the resolved representation, not from the
	// requested `.jsonl` path (which no longer exists on disk).
	sourceID, err := threadForkSourceID(&ThreadForkParams{Path: &logicalPath})
	if err != nil {
		t.Fatalf("threadForkSourceID() error = %v", err)
	}
	if sourceID != session.ThreadID("compressed-fork-thread") {
		t.Fatalf("threadForkSourceID() = %q, want %q", sourceID, "compressed-fork-thread")
	}

}

// Rust inspects the requested metadata before resolving, so a directory must keep
// reporting the directory error instead of a compressed-resolution miss.
func TestRouterForkCompressedRolloutKeepsDirectoryErrorLikeRust(t *testing.T) {
	store := session.NewStore(t.TempDir())
	router := NewRouter(store)
	path := t.TempDir()

	if _, err := threadForkSourceID(&ThreadForkParams{Path: &path}); err == nil || !strings.Contains(err.Error(), "path is a directory") {
		t.Fatalf("threadForkSourceID() error = %v, want the directory error", err)
	}

	response := router.Handle(requestWithParams(t, IntID(1), MethodThreadFork, ThreadForkParams{
		ThreadID: "thread-a",
		Path:     &path,
	}))
	if response.Error == nil || !strings.Contains(response.Error.Message, "path is a directory") || strings.Contains(response.Error.Message, "Is a directory") {
		t.Fatalf("fork directory error = %+v", response.Error)
	}
}

// A path with neither representation must still fail with the not-found text the
// resume entry point already uses, because both entry points share Rust's single
// `resolve_requested_rollout_path` helper.
func TestRouterForkCompressedRolloutKeepsMissingFileErrorLikeRust(t *testing.T) {
	store := session.NewStore(t.TempDir())
	router := NewRouter(store)
	missing := filepath.Join(t.TempDir(), "rollout-2026-01-02T10-00-00-missing.jsonl")
	// The fork entry point adopts the wording the resume entry point already uses for
	// the same Rust helper; `ErrInvalidRequest` carries Go's own prefix and maps to
	// invalid_params on the wire (appserver/router.go:errorCode).
	want := fmt.Sprintf("%s: failed to resolve rollout path `%s`: file does not exist", ErrInvalidRequest, missing)

	if _, err := threadForkSourceID(&ThreadForkParams{Path: &missing}); err == nil || !strings.Contains(err.Error(), "file does not exist") {
		t.Fatalf("threadForkSourceID() error = %v, want the not-found error", err)
	}

	response := router.Handle(requestWithParams(t, IntID(1), MethodThreadFork, ThreadForkParams{
		ThreadID: "thread-a",
		Path:     &missing,
	}))
	if response.Error == nil || response.Error.Code != JSONRPCInvalidParamsErrorCode || response.Error.Message != want {
		t.Fatalf("fork missing-file error = %+v, want code %d message %q", response.Error, JSONRPCInvalidParamsErrorCode, want)
	}
}
