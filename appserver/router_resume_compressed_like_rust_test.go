package appserver

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/klauspost/compress/zstd"

	"codex_go/rollout"
	"codex_go/session"
)

// compressResumeRolloutLikeStartupCompression replaces the plain rollout at path
// with the `.zst` sibling that startup compression leaves behind, using the same
// pinned encoder the rollout package uses for its own compressed fixtures.
func compressResumeRolloutLikeStartupCompression(t *testing.T, path string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read plain rollout: %v", err)
	}
	encoder, err := zstd.NewWriter(nil)
	if err != nil {
		t.Fatalf("zstd.NewWriter() error = %v", err)
	}
	defer encoder.Close()
	if err := os.WriteFile(path+".zst", encoder.EncodeAll(data, nil), 0o600); err != nil {
		t.Fatalf("write compressed rollout: %v", err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatalf("remove plain rollout: %v", err)
	}
}

// resumeRolloutFixture writes a real rollout through the production recorder and
// returns the logical `.jsonl` path a client receives from thread/list.
func resumeRolloutFixture(t *testing.T, home string, threadID string) string {
	t.Helper()
	recorder, err := rollout.NewRecorder(&rollout.CreateParams{
		CodexHome:     home,
		SessionID:     threadID + "-session",
		ThreadID:      threadID,
		Source:        "cli",
		CWD:           "/compressed/repo",
		ModelProvider: "openai",
		Now:           fixedTime(),
	})
	if err != nil {
		t.Fatalf("rollout.NewRecorder() error = %v", err)
	}
	if err := recorder.AppendItem(rollout.Item{ID: "user-1", Type: "message", Role: "user", Text: "compressed history"}); err != nil {
		t.Fatalf("AppendItem() error = %v", err)
	}
	if err := recorder.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	return recorder.Path()
}

// Mirrors Rust #54008: `resolve_requested_rollout_path`
// (thread-store/src/local/read_thread.rs:202-263) resolves the requested path to
// whichever representation currently exists, so the logical `.jsonl` path
// returned by thread/list still resumes after startup compression rewrote the
// rollout as `<path>.zst`.
func TestRouterResumeCompressedRolloutByLogicalPathLikeRust(t *testing.T) {
	store := session.NewStore(t.TempDir())
	router := NewRouter(store)
	logicalPath := resumeRolloutFixture(t, t.TempDir(), "compressed-thread")
	compressResumeRolloutLikeStartupCompression(t, logicalPath)
	if _, err := os.Stat(logicalPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("plain rollout still present after compression: err = %v", err)
	}
	if _, err := os.Stat(logicalPath + ".zst"); err != nil {
		t.Fatalf("compressed rollout missing: %v", err)
	}

	response := router.Handle(requestWithParams(t, IntID(1), MethodThreadResume, ThreadResumeParams{
		ThreadID: "not-the-rollout-thread",
		Path:     &logicalPath,
	}))
	if response.Error != nil {
		t.Fatalf("resume by logical path error = %+v", response.Error)
	}
	resume := response.Result.(*ThreadResumeResponse)
	if resume.Thread == nil || resume.Thread.ID != "compressed-thread" {
		t.Fatalf("resume thread = %+v", resume.Thread)
	}
	if len(resume.Thread.Turns) != 1 {
		t.Fatalf("resume turns = %+v, want the compressed rollout's single turn", resume.Thread.Turns)
	}
	if len(resume.Thread.Turns[0].Items) != 1 || resume.Thread.Turns[0].Items[0].Text != "compressed history" {
		t.Fatalf("resume turn items = %+v", resume.Thread.Turns[0].Items)
	}
	// The response keeps echoing the logical path the client sent (Rust #54008:
	// "Exercise the logical .jsonl paths returned by thread/list after startup
	// compression").
	if resume.Thread.Path == nil || *resume.Thread.Path != logicalPath {
		t.Fatalf("resume thread Path = %+v, want %q", resume.Thread.Path, logicalPath)
	}
}

// Rust inspects the requested metadata before resolving, so a directory must keep
// reporting the directory error instead of a compressed-resolution miss.
func TestRouterResumeCompressedRolloutKeepsDirectoryErrorLikeRust(t *testing.T) {
	store := session.NewStore(t.TempDir())
	router := NewRouter(store)
	path := t.TempDir()

	response := router.Handle(requestWithParams(t, IntID(1), MethodThreadResume, ThreadResumeParams{
		ThreadID: "thread-a",
		Path:     &path,
	}))
	if response.Error == nil || !strings.Contains(response.Error.Message, "path is a directory") || strings.Contains(response.Error.Message, "Is a directory") {
		t.Fatalf("resume directory error = %+v", response.Error)
	}
}

// A path with neither representation must still fail with Rust's not-found text.
func TestRouterResumeCompressedRolloutKeepsMissingFileErrorLikeRust(t *testing.T) {
	store := session.NewStore(t.TempDir())
	router := NewRouter(store)
	missing := filepath.Join(t.TempDir(), "rollout-2026-01-02T10-00-00-missing.jsonl")

	response := router.Handle(requestWithParams(t, IntID(1), MethodThreadResume, ThreadResumeParams{
		ThreadID: "thread-a",
		Path:     &missing,
	}))
	if response.Error == nil || !strings.Contains(response.Error.Message, "file does not exist") {
		t.Fatalf("resume missing-file error = %+v", response.Error)
	}
}
