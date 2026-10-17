package appserver

import (
	"os"
	"path/filepath"
	"testing"

	"codex_go/rollout"
	"codex_go/session"
)

// Mirrors Rust `archive_thread_with_paths`
// (codex-rs/thread-store/src/local/archive_thread.rs, #41908): the router must
// archive every active rollout owned by the thread, not just the selected one.
func TestRouterArchiveMovesEveryOwnedActiveRolloutLikeRust(t *testing.T) {
	store := session.NewStore(t.TempDir())
	router := NewRouter(store)
	createRecord(t, store, "thread-a", fixedTime())
	record, err := store.Read("thread-a", true, true)
	if err != nil {
		t.Fatalf("read created: %v", err)
	}
	if err := router.createThreadRollout(record, fixedTime()); err != nil {
		t.Fatalf("create rollout: %v", err)
	}
	selected, err := rollout.FindThreadPath(store.Root(), "thread-a", false)
	if err != nil {
		t.Fatalf("FindThreadPath(active) error = %v", err)
	}
	sessionsDir := filepath.Dir(selected)
	replacementID := "0192f0aa-0000-7000-8000-000000000099"
	replacementPath := filepath.Join(sessionsDir, "rollout-2025-01-03T12-00-00-"+"thread-a_"+replacementID+".jsonl")
	recorder, err := rollout.NewRecorder(&rollout.CreateParams{
		CodexHome: store.Root(),
		ThreadID:  "thread-a",
		Source:    "cli",
		CWD:       "/repo",
		Now:       fixedTime(),
		Path:      replacementPath,
	})
	if err != nil {
		t.Fatalf("NewRecorder() error = %v", err)
	}
	if err := recorder.AppendItem(rollout.Item{ID: "user-2", Type: "message", Role: "user", Text: "replacement"}); err != nil {
		t.Fatalf("AppendItem() error = %v", err)
	}
	if err := recorder.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	archive := router.Handle(requestWithParams(t, IntID(1), MethodThreadArchive, ThreadArchiveParams{ThreadID: "thread-a"}))
	if archive.Error != nil {
		t.Fatalf("archive error: %+v", archive.Error)
	}

	archiveDir := filepath.Join(store.Root(), rollout.ArchivedSessionsSubdir)
	for _, path := range []string{selected, replacementPath} {
		name := filepath.Base(path)
		if _, err := os.Stat(filepath.Join(archiveDir, name)); err != nil {
			t.Fatalf("archived rollout %q missing: %v", name, err)
		}
		if _, err := os.Stat(filepath.Join(sessionsDir, name)); err == nil {
			t.Fatalf("rollout %q is still active after archiving", name)
		}
	}
	if _, err := rollout.FindThreadPath(store.Root(), "thread-a", false); err == nil {
		t.Fatal("active rollout still exists after archiving")
	}
}
