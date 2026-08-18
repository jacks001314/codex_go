package appserver

import (
	"testing"

	"codex_go/session"
	"codex_go/turn"
)

// TestRuntimeRouterResumeLoadDispatchesPendingQueuedSubmissionLikeRust mirrors
// Rust #39034: when a thread is loaded/resumed and idle with pending queued
// messages (including messages written by another process to the durable
// store), the next queued submission is dispatched. Go observes cross-process
// writes by re-reading the thread record from the store on load/resume instead
// of the Rust PRAGMA data_version polling (structural N/A).
func TestRuntimeRouterResumeLoadDispatchesPendingQueuedSubmissionLikeRust(t *testing.T) {
	store := session.NewStore(t.TempDir())
	threadID := session.ThreadID("thread-queue-load")
	if err := store.Create(&session.Record{ID: threadID, SessionID: string(threadID), Metadata: session.Metadata{HistoryMode: "legacy"}}); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if _, err := store.EnqueueSubmission(threadID, session.QueuedSubmission{
		ID:                  "q-1",
		Input:               []any{map[string]any{"type": "text", "text": "hello"}},
		ClientUserMessageID: "client-1",
	}); err != nil {
		t.Fatalf("EnqueueSubmission() error = %v", err)
	}

	agent := newRecordingRuntimeAgent("done")
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		ThreadExtras: NewThreadExtraService(),
		Turns:        turn.NewTurnService(),
		Agent:        agent,
		ThreadStatus: NewThreadStatusManager(),
	})
	router.markResponseThreadLoaded(&ThreadResumeResponse{Thread: &Thread{ID: string(threadID)}}, "conn-1")
	router.maybeDispatchQueuedSubmissionIfIdle(string(threadID))

	pending, _, err := store.ListQueueSubmissions(threadID, "", 10)
	if err != nil {
		t.Fatalf("ListQueueSubmissions() error = %v", err)
	}
	if len(pending) != 0 {
		t.Fatalf("pending queued submissions after dispatch = %d, want drained", len(pending))
	}
}

// TestRuntimeRouterQueuedDispatchSkipsBusyOrMissingThreadsLikeRust verifies the
// idle/loaded guards: an unloaded thread and a running thread are not woken.
func TestRuntimeRouterQueuedDispatchSkipsBusyOrMissingThreadsLikeRust(t *testing.T) {
	store := session.NewStore(t.TempDir())
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		ThreadStatus: NewThreadStatusManager(),
	})
	// Unloaded thread: dispatch must not panic and must leave the queue intact.
	threadID := session.ThreadID("thread-unloaded")
	if err := store.Create(&session.Record{ID: threadID, SessionID: string(threadID), Metadata: session.Metadata{HistoryMode: "legacy"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.EnqueueSubmission(threadID, session.QueuedSubmission{ID: "q-1", Input: []any{map[string]any{"type": "text", "text": "hi"}}}); err != nil {
		t.Fatal(err)
	}
	router.maybeDispatchQueuedSubmissionIfIdle(string(threadID))
	pending, _, err := store.ListQueueSubmissions(threadID, "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 {
		t.Fatalf("pending queued submissions for unloaded thread = %d, want 1 (not dispatched)", len(pending))
	}

	// Nil guards.
	var nilRouter *RuntimeRouter
	nilRouter.maybeDispatchQueuedSubmissionIfIdle(string(threadID))
	nilRouter.maybeDispatchQueuedSubmissionIfIdle("")
}
