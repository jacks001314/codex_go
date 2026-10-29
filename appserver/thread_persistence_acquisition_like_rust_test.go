package appserver

import (
	"os"
	"path/filepath"
	"testing"

	"codex_go/rollout"
	"codex_go/session"
)

// Rust #45487: session initialization keeps the acquired persistence writer
// until it commits, so a cancelled or failed initialization discards it instead
// of leaving the writer owned by a thread the client never received.
func TestLiveThreadRetentionDiscardsWriterUntilCommitLikeRust(t *testing.T) {
	store := session.NewStore(t.TempDir())
	manager := NewThreadManager(nil)
	record := &session.Record{
		ID:        "thread-retention",
		SessionID: "thread-retention",
		Metadata:  session.Metadata{HistoryMode: string(ThreadHistoryPaginated)},
	}
	if err := store.Create(record); err != nil {
		t.Fatalf("store.Create() error = %v", err)
	}

	retention, err := manager.AcquireLiveThreadRetention(store, record)
	if err != nil {
		t.Fatalf("AcquireLiveThreadRetention() error = %v", err)
	}
	if retention == nil {
		t.Fatal("retention = nil, want an owned acquisition")
	}
	if manager.HasLiveThread(record.ID) {
		t.Fatal("acquisition registered the thread before it was committed")
	}
	if _, err := store.AcquireWriter(record.ID); err == nil {
		t.Fatal("writer was not held while the acquisition was in flight")
	}

	if err := retention.Discard(); err != nil {
		t.Fatalf("Discard() error = %v", err)
	}
	if manager.HasLiveThread(record.ID) {
		t.Fatal("discarded acquisition left a live thread")
	}
	writer, err := store.AcquireWriter(record.ID)
	if err != nil {
		t.Fatalf("writer was not released after discard: %v", err)
	}
	_ = writer.Close()

	retention, err = manager.AcquireLiveThreadRetention(store, record)
	if err != nil {
		t.Fatalf("second AcquireLiveThreadRetention() error = %v", err)
	}
	retention.Commit()
	liveThread := manager.LiveThread(record.ID)
	if liveThread == nil || !liveThread.OwnsWriter() {
		t.Fatalf("committed retention = %#v, want a live thread owning its writer", liveThread)
	}
	if _, err := store.AcquireWriter(record.ID); err == nil {
		t.Fatal("committed retention released its writer")
	}
	manager.ReleaseLiveThreads([]session.ThreadID{record.ID})
	writer, err = store.AcquireWriter(record.ID)
	if err != nil {
		t.Fatalf("writer was not released after release: %v", err)
	}
	_ = writer.Close()
}

func TestLiveThreadRetentionIsIdempotentForLiveThreadsLikeRust(t *testing.T) {
	store := session.NewStore(t.TempDir())
	manager := NewThreadManager(nil)
	record := &session.Record{ID: "thread-already-live", SessionID: "thread-already-live"}
	if err := store.Create(record); err != nil {
		t.Fatalf("store.Create() error = %v", err)
	}
	if err := manager.RetainLiveThread(store, record); err != nil {
		t.Fatalf("RetainLiveThread() error = %v", err)
	}
	retention, err := manager.AcquireLiveThreadRetention(store, record)
	if err != nil {
		t.Fatalf("AcquireLiveThreadRetention() error = %v", err)
	}
	if retention != nil {
		t.Fatalf("retention = %#v, want nil for an already live thread", retention)
	}
	if !manager.HasLiveThread(record.ID) {
		t.Fatal("already live thread was dropped")
	}
}

// Rust #45487 uses the guard for inherited-context thread creation, which Go
// models as a supplied-history resume.
func TestThreadResumeHistoryAcquiresPersistenceLikeRust(t *testing.T) {
	store := session.NewStore(t.TempDir())
	router := NewRouter(store)
	t.Cleanup(func() { _ = router.Close() })

	response := router.Handle(requestWithParams(t, IntID(1), MethodThreadResume, ThreadResumeParams{
		ThreadID:   "supplied",
		HistorySet: true,
		History: []ThreadResumeHistoryItem{ThreadResumeHistoryItem(
			[]byte(`{"type":"message","role":"user","content":[{"type":"input_text","text":"supplied"}]}`),
		)},
	}))
	if response.Error != nil {
		t.Fatalf("supplied-history resume error = %+v", response.Error)
	}
	threadID := session.ThreadID(response.Result.(*ThreadResumeResponse).Thread.ID)
	if liveThread := router.threads.LiveThread(threadID); liveThread == nil {
		t.Fatal("supplied-history resume did not acquire thread persistence")
	}
}

// Rust #45487: a failed inherited-context creation discards the writer instead
// of leaving persistence acquired for a thread that was never returned.
func TestThreadResumeHistoryDiscardsPersistenceOnFailureLikeRust(t *testing.T) {
	home := t.TempDir()
	store := session.NewStore(home)
	router := NewRouter(store)
	t.Cleanup(func() { _ = router.Close() })
	// Rollout creation is the first step that can fail after persistence is
	// acquired; a file where the sessions directory belongs forces it.
	if err := os.WriteFile(filepath.Join(home, rollout.SessionsSubdir), []byte("not a directory"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	response := router.Handle(requestWithParams(t, IntID(1), MethodThreadResume, ThreadResumeParams{
		ThreadID:   "supplied",
		HistorySet: true,
		History: []ThreadResumeHistoryItem{ThreadResumeHistoryItem(
			[]byte(`{"type":"message","role":"user","content":[{"type":"input_text","text":"supplied"}]}`),
		)},
	}))
	if response.Error == nil {
		t.Fatal("supplied-history resume succeeded despite an unwritable sessions directory")
	}
	if liveThreads := router.threads.LiveThreadCount(); liveThreads != 0 {
		t.Fatalf("retained live threads after failed initialization = %d", liveThreads)
	}
	page, err := store.List(session.ListOptions{})
	if err != nil {
		t.Fatalf("store.List() error = %v", err)
	}
	if len(page.Records) != 0 {
		t.Fatalf("records after failed initialization = %d, want 0", len(page.Records))
	}
}
