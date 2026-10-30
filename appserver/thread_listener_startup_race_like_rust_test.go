package appserver

import (
	"strings"
	"testing"
	"time"

	"codex_go/session"
)

// TestThreadResumeWaitsForPendingUnloadLikeRust pins Rust #57787: a cold resume
// must not install a runtime while an earlier idle eviction is still cleaning
// up. The resume blocks on the in-flight unload and proceeds only afterwards.
func TestThreadResumeWaitsForPendingUnloadLikeRust(t *testing.T) {
	router, _ := newIdleLifecycleTestRouter(t, 0)

	done := make(chan struct{})
	router.idleLifecycle.mu.Lock()
	router.idleLifecycle.ensureMapsLocked()
	router.idleLifecycle.pending["child"] = done
	router.idleLifecycle.mu.Unlock()

	acquired := make(chan func(), 1)
	go func() {
		release, err := router.acquireThreadResidencyPin("child")
		if err != nil {
			t.Errorf("acquireThreadResidencyPin() error = %v", err)
			acquired <- nil
			return
		}
		acquired <- release
	}()
	select {
	case <-acquired:
		t.Fatal("resume acquired residency while an idle unload was still pending")
	case <-time.After(50 * time.Millisecond):
	}

	router.idleLifecycle.mu.Lock()
	delete(router.idleLifecycle.pending, "child")
	close(done)
	router.idleLifecycle.mu.Unlock()

	select {
	case release := <-acquired:
		if release == nil {
			t.Fatal("resume did not acquire residency after the unload finished")
		}
		release()
	case <-time.After(2 * time.Second):
		t.Fatal("resume did not resume after the pending unload finished")
	}
}

// TestThreadResidencyPinBlocksIdleUnloadLikeRust pins the second half of Rust
// #57787: while a resume holds the thread, an idle unload that fires for the
// same thread must leave the runtime alone instead of dropping it between
// runtime install and subscription.
func TestThreadResidencyPinBlocksIdleUnloadLikeRust(t *testing.T) {
	router, _ := newIdleLifecycleTestRouter(t, 0)
	retainIdleLifecycleThread(t, router, "child")
	router.subscribeThreadConnection("child", "conn-owner")

	release, err := router.acquireThreadResidencyPin("child")
	if err != nil {
		t.Fatalf("acquireThreadResidencyPin() error = %v", err)
	}
	// A stale timer fires while the resume is in flight; the pin keeps the
	// runtime resident.
	if !router.unsubscribeThreadConnection("child", "conn-owner") {
		t.Fatal("unsubscribeThreadConnection() = false")
	}
	router.idleLifecycle.mu.Lock()
	generation := router.idleLifecycle.generation["child"]
	router.idleLifecycle.mu.Unlock()
	router.unloadIdleChildThread("child", generation)
	if router.threads.LiveThread(session.ThreadID("child")) == nil {
		t.Fatal("idle unload evicted a thread pinned by an in-flight resume")
	}

	release()
	waitForIdleUnload(t, router, "child")
}

// TestInstallThreadSubscriptionRejectsEvictedRuntimeLikeRust pins the
// registration re-check from Rust #57787: a runtime that disappeared while the
// request was starting up is reported as closing instead of being attached to a
// dead thread.
func TestInstallThreadSubscriptionRejectsEvictedRuntimeLikeRust(t *testing.T) {
	router, store := newIdleLifecycleTestRouter(t, 0)
	now := time.Now().UTC()
	record := &session.Record{ID: "regressed", SessionID: "regressed", CreatedAt: now, UpdatedAt: now, Metadata: session.Metadata{CWD: t.TempDir()}}
	if err := store.Save(record); err != nil {
		t.Fatal(err)
	}

	err := router.installThreadSubscription(&ThreadResumeResponse{Thread: &Thread{ID: "regressed"}}, "conn-1")
	if err == nil || !strings.Contains(err.Error(), "is closing; retry thread/resume after the thread is closed") {
		t.Fatalf("installThreadSubscription() error = %v, want thread-closing error", err)
	}
	if subscribers := router.threads.Subscribers("regressed"); len(subscribers) != 0 {
		t.Fatalf("subscribers = %v, want none", subscribers)
	}

	retainIdleLifecycleThread(t, router, "regressed")
	if err := router.installThreadSubscription(&ThreadResumeResponse{Thread: &Thread{ID: "regressed"}}, "conn-1"); err != nil {
		t.Fatalf("installThreadSubscription() with a registered runtime error = %v", err)
	}
	if subscribers := router.threads.Subscribers("regressed"); len(subscribers) != 1 || subscribers[0] != "conn-1" {
		t.Fatalf("subscribers = %v, want [conn-1]", subscribers)
	}
}
