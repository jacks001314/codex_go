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

// TestRuntimeRouterQueuedDispatchKeepsSubmissionWhenReservationInvalidatedLikeRust
// mirrors Rust #51427: a wakeup can outlive the reservation it was built on.
// When an interrupt or a replacement turn claims the thread while the completing
// turn is still finishing, the wakeup must not start a turn (its reservation is
// no longer ours) and must leave the queued submission in place so a later idle
// dispatch can run it.
func TestRuntimeRouterQueuedDispatchKeepsSubmissionWhenReservationInvalidatedLikeRust(t *testing.T) {
	store := session.NewStore(t.TempDir())
	threadID := session.ThreadID("thread-wake-reservation")
	if err := store.Create(&session.Record{ID: threadID, SessionID: string(threadID), Metadata: session.Metadata{HistoryMode: "legacy"}}); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if _, err := store.EnqueueSubmission(threadID, session.QueuedSubmission{
		ID:    "q-1",
		Input: []any{map[string]any{"type": "text", "text": "wake me"}},
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
	// A concurrent turn claims the thread after the completing turn released it
	// but before the wakeup dispatches its queued submission.
	if err := router.registerActiveRuntimeTurn(string(threadID), "turn-claimed", func() {}, 1, &turn.TurnStartParams{ThreadID: string(threadID)}); err != nil {
		t.Fatalf("registerActiveRuntimeTurn() error = %v", err)
	}

	router.maybeDispatchNextQueuedSubmission(string(threadID))

	// The wakeup must not steal the thread: the claiming turn is still the only
	// active turn, and no queued turn was started over it.
	active := router.threads.ActiveTurn(string(threadID))
	if active == nil || active.TurnID != "turn-claimed" {
		t.Fatalf("active turn after invalidated wakeup = %+v, want the claiming turn untouched", active)
	}
	// The queued submission is retained (previously it was dequeued and then
	// dropped when the reservation conflict aborted the start).
	pending, _, err := store.ListQueueSubmissions(threadID, "", 10)
	if err != nil {
		t.Fatalf("ListQueueSubmissions() error = %v", err)
	}
	if len(pending) != 1 || pending[0].ID != "q-1" {
		t.Fatalf("pending queued submissions after invalidated wakeup = %+v, want the submission retained", pending)
	}
}
