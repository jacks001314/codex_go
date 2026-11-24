package appserver

import (
	"errors"
	"testing"

	"codex_go/session"
)

// TestQueuedSessionStartHookKeepsEventUntilHandledLikeRust pins Rust #63523: a
// SessionStart event stays queued until its outcome is handled, so an
// interrupted hook is retried by the next turn instead of being skipped.
func TestQueuedSessionStartHookKeepsEventUntilHandledLikeRust(t *testing.T) {
	store := session.NewStore(t.TempDir())
	if err := store.Save(&session.Record{ID: "thread-1"}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	router := NewRuntimeRouter(RuntimeServices{ThreadRouter: NewRouter(store), HookRunner: NewHookRunner()})
	if err := router.markThreadPendingSessionStartSource("thread-1", SessionStartSourceStartup); err != nil {
		t.Fatalf("markThreadPendingSessionStartSource() error = %v", err)
	}

	interrupted := errors.New("turn torn down during the start hook")
	calls := 0
	err := router.runQueuedSessionStartHook("thread-1", func(source SessionStartSource, record *session.Record) (bool, error) {
		calls++
		if source != SessionStartSourceStartup {
			t.Fatalf("source = %q, want startup", source)
		}
		if record == nil || record.ID != "thread-1" {
			t.Fatalf("record = %#v", record)
		}
		return true, interrupted
	})
	if !errors.Is(err, interrupted) {
		t.Fatalf("runQueuedSessionStartHook() error = %v, want the hook failure", err)
	}
	if _, _, ok, peekErr := router.peekPendingSessionStartSource("thread-1"); peekErr != nil || !ok {
		t.Fatalf("a retryable failure dropped the event: ok=%v err=%v", ok, peekErr)
	}

	// The next turn runs the same event and clears it once the outcome exists.
	err = router.runQueuedSessionStartHook("thread-1", func(source SessionStartSource, _ *session.Record) (bool, error) {
		calls++
		if source != SessionStartSourceStartup {
			t.Fatalf("retry source = %q, want startup", source)
		}
		return false, nil
	})
	if err != nil {
		t.Fatalf("retry error = %v", err)
	}
	if _, _, ok, peekErr := router.peekPendingSessionStartSource("thread-1"); peekErr != nil || ok {
		t.Fatalf("a handled event stayed queued: ok=%v err=%v", ok, peekErr)
	}
	if calls != 2 {
		t.Fatalf("hook attempts = %d, want 2", calls)
	}

	// A handled rejection (a blocking hook) is an outcome too, so it clears the
	// event while still reporting the failure to the caller.
	if err := router.markThreadPendingSessionStartSource("thread-1", SessionStartSourceResume); err != nil {
		t.Fatalf("markThreadPendingSessionStartSource() error = %v", err)
	}
	blocked := errors.New("hook blocked the turn")
	err = router.runQueuedSessionStartHook("thread-1", func(SessionStartSource, *session.Record) (bool, error) {
		return false, blocked
	})
	if !errors.Is(err, blocked) {
		t.Fatalf("blocked error = %v", err)
	}
	if _, _, ok, peekErr := router.peekPendingSessionStartSource("thread-1"); peekErr != nil || ok {
		t.Fatalf("a handled rejection stayed queued: ok=%v err=%v", ok, peekErr)
	}

	// Nothing queued means nothing to run.
	err = router.runQueuedSessionStartHook("thread-1", func(SessionStartSource, *session.Record) (bool, error) {
		t.Fatal("an empty queue ran the hook")
		return false, nil
	})
	if err != nil {
		t.Fatalf("empty queue error = %v", err)
	}
}
