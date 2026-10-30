package appserver

import (
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	"codex_go/config"
	"codex_go/session"
)

func newIdleLifecycleTestRouter(t *testing.T, delaySeconds int) (*RuntimeRouter, *session.Store) {
	t.Helper()
	home := t.TempDir()
	if err := os.WriteFile(config.ConfigPath(home), []byte(fmt.Sprintf("thread_unload_delay_secs = %d\n", delaySeconds)), 0o600); err != nil {
		t.Fatal(err)
	}
	store := session.NewStore(t.TempDir())
	now := time.Now().UTC()
	for _, record := range []*session.Record{
		{ID: "root", SessionID: "root", CreatedAt: now, UpdatedAt: now, Metadata: session.Metadata{CWD: t.TempDir(), AgentPath: "/root", MultiAgentVersion: "v2"}},
		{ID: "child", SessionID: "child", ParentThreadID: "root", CreatedAt: now, UpdatedAt: now, Metadata: session.Metadata{CWD: t.TempDir(), AgentPath: "/root/child", AgentDepth: 1, MultiAgentVersion: "v2"}},
	} {
		if err := store.Save(record); err != nil {
			t.Fatal(err)
		}
	}
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		Config:       config.NewConfigService(home),
	})
	t.Cleanup(func() { _ = router.services.ThreadRouter.Close() })
	return router, store
}

func retainIdleLifecycleThread(t *testing.T, router *RuntimeRouter, threadID string) {
	t.Helper()
	record, err := router.services.ThreadRouter.readThreadRecord(session.ThreadID(threadID), false, false)
	if err != nil || record == nil {
		t.Fatalf("readThreadRecord(%s) = %#v, %v", threadID, record, err)
	}
	if err := router.services.ThreadRouter.retainLiveThread(record); err != nil {
		t.Fatalf("retainLiveThread(%s) error = %v", threadID, err)
	}
}

func retainEphemeralIdleLifecycleThread(t *testing.T, router *RuntimeRouter, threadID string) {
	t.Helper()
	record := &session.Record{
		ID:        session.ThreadID(threadID),
		SessionID: threadID,
		Metadata: session.Metadata{
			CWD:   t.TempDir(),
			Extra: map[string]any{"ephemeral": true},
		},
	}
	if !router.saveEphemeralThreadRecord(record) {
		t.Fatalf("saveEphemeralThreadRecord(%s) = false", threadID)
	}
	if err := router.services.ThreadRouter.retainLiveThread(record); err != nil {
		t.Fatalf("retainLiveThread(%s) error = %v", threadID, err)
	}
}

func waitForIdleUnload(t *testing.T, router *RuntimeRouter, threadID string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for router.threads.LiveThread(session.ThreadID(threadID)) != nil {
		if time.Now().After(deadline) {
			t.Fatalf("thread %s was not unloaded", threadID)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestIdleChildUnloadsAfterDelayAndUnsubscribeLikeRust(t *testing.T) {
	router, _ := newIdleLifecycleTestRouter(t, 0)
	retainIdleLifecycleThread(t, router, "child")
	router.subscribeThreadConnection("child", "conn-1")
	router.scheduleThreadIdleUnload("child")
	time.Sleep(100 * time.Millisecond)
	if router.threads.LiveThread("child") == nil {
		t.Fatal("a subscribed child was evicted")
	}
	if !router.unsubscribeThreadConnection("child", "conn-1") {
		t.Fatal("unsubscribe returned false")
	}
	waitForIdleUnload(t, router, "child")
}

func TestIdleChildCreatedWithoutClientDisconnectsLikeRust(t *testing.T) {
	router, _ := newIdleLifecycleTestRouter(t, 0)
	retainIdleLifecycleThread(t, router, "child")
	waitForIdleUnload(t, router, "child")
}

// Rust #42320: gaining a subscriber resets the unload countdown even when the
// thread had already been scheduled with a zero delay.
func TestIdleLifecycleResubscribeResetsUnloadLikeRust(t *testing.T) {
	router, _ := newIdleLifecycleTestRouter(t, 0)
	retainIdleLifecycleThread(t, router, "child")
	router.scheduleThreadIdleUnload("child")
	router.subscribeThreadConnection("child", "conn-resubscribe")
	time.Sleep(120 * time.Millisecond)
	if router.threads.LiveThread("child") == nil {
		t.Fatal("resubscribed child was evicted")
	}
	if !router.unsubscribeThreadConnection("child", "conn-resubscribe") {
		t.Fatal("unsubscribe returned false")
	}
	waitForIdleUnload(t, router, "child")
}

func TestIdleLifecycleKeepsPersistentRootAndEphemeralChildThreadsLikeRust(t *testing.T) {
	router, _ := newIdleLifecycleTestRouter(t, 0)
	retainIdleLifecycleThread(t, router, "root")
	retainIdleLifecycleThread(t, router, "child")
	router.ephemeralThreads["child"] = &session.Record{ID: "child", SessionID: "child", ParentThreadID: "root"}
	router.scheduleThreadIdleUnload("root")
	router.scheduleThreadIdleUnload("child")
	time.Sleep(150 * time.Millisecond)
	if router.threads.LiveThread("root") == nil {
		t.Fatal("root thread was locally evicted")
	}
	if router.threads.LiveThread("child") == nil {
		t.Fatal("ephemeral child was evicted")
	}
}

// Rust #50380/#52081: an ephemeral client root has no durable record to resume,
// so an idle, unsubscribed root is reclaimed instead of staying loaded forever.
func TestIdleLifecycleReclaimsEphemeralRootThreadLikeRust(t *testing.T) {
	router, _ := newIdleLifecycleTestRouter(t, 0)
	retainEphemeralIdleLifecycleThread(t, router, "ephemeral-root")
	router.scheduleThreadIdleUnload("ephemeral-root")
	waitForIdleUnload(t, router, "ephemeral-root")
	if _, ok := router.ephemeralThreadRecord(session.ThreadID("ephemeral-root"), false); ok {
		t.Fatal("an unloaded ephemeral root kept its in-memory record")
	}
}

func TestIdleLifecycleResidencyPinBlocksEvictionLikeRust(t *testing.T) {
	router, _ := newIdleLifecycleTestRouter(t, 0)
	retainIdleLifecycleThread(t, router, "child")
	release := router.pinThreadResidency("child")
	router.scheduleThreadIdleUnload("child")
	time.Sleep(150 * time.Millisecond)
	if router.threads.LiveThread("child") == nil {
		t.Fatal("pinned child was evicted")
	}
	release()
	waitForIdleUnload(t, router, "child")
}

func TestIdleLifecycleWaitsForPendingCleanupLikeRust(t *testing.T) {
	router, _ := newIdleLifecycleTestRouter(t, 0)
	done := make(chan struct{})
	router.idleLifecycle.mu.Lock()
	router.idleLifecycle.ensureMapsLocked()
	router.idleLifecycle.pending["child"] = done
	router.idleLifecycle.mu.Unlock()

	waited := make(chan error, 1)
	go func() { waited <- router.awaitPendingThreadUnload("child") }()
	select {
	case err := <-waited:
		t.Fatalf("resume did not wait for cleanup: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	close(done)
	select {
	case err := <-waited:
		if err != nil {
			t.Fatalf("awaitPendingThreadUnload() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("awaitPendingThreadUnload did not return after cleanup")
	}
}

func TestIdleChildWaitsConfiguredDelayLikeRust(t *testing.T) {
	router, _ := newIdleLifecycleTestRouter(t, 1)
	retainIdleLifecycleThread(t, router, "child")
	router.scheduleThreadIdleUnload("child")
	time.Sleep(200 * time.Millisecond)
	if router.threads.LiveThread("child") == nil {
		t.Fatal("child was evicted before the configured delay")
	}
	waitForIdleUnload(t, router, "child")
}

func TestIdleUnloadPreservesQueuedMailLikeRust(t *testing.T) {
	router, _ := newIdleLifecycleTestRouter(t, 0)
	retainIdleLifecycleThread(t, router, "child")
	router.enqueueRuntimeAgentMessage("child", map[string]any{"kind": "followup", "text": "hello"})
	router.scheduleThreadIdleUnload("child")
	waitForIdleUnload(t, router, "child")
	queued := router.drainRuntimeAgentMessages("child")
	if len(queued) != 1 {
		t.Fatalf("queued mail = %#v, want the unread followup", queued)
	}
}

func TestIdleEphemeralThreadWithZeroOverrideUnloadsLikeRust(t *testing.T) {
	router, _ := newIdleLifecycleTestRouter(t, 1800)
	retainIdleLifecycleThread(t, router, "child")
	router.ephemeralThreads["child"] = &session.Record{
		ID: "child", SessionID: "child", ParentThreadID: "root",
		Metadata: session.Metadata{ThreadSource: "composer_predictions", AgentPath: "/root/child"},
	}
	router.setThreadUnloadDelayOverride("child", 0, true)
	router.scheduleThreadIdleUnload("child")
	waitForIdleUnload(t, router, "child")
}

func TestIdleEphemeralSubagentStaysLoadedEvenWithZeroOverrideLikeRust(t *testing.T) {
	router, _ := newIdleLifecycleTestRouter(t, 1800)
	retainIdleLifecycleThread(t, router, "child")
	router.ephemeralThreads["child"] = &session.Record{
		ID: "child", SessionID: "child", ParentThreadID: "root",
		Metadata: session.Metadata{ThreadSource: "composer_predictions", Source: "subagent", AgentDepth: 1, AgentPath: "/root/child"},
	}
	router.setThreadUnloadDelayOverride("child", 0, true)
	router.scheduleThreadIdleUnload("child")
	time.Sleep(150 * time.Millisecond)
	if router.threads.LiveThread("child") == nil {
		t.Fatal("ephemeral composer-prediction subagent was evicted")
	}
}

func TestThreadUnloadDelayOverrideLikeRust(t *testing.T) {
	router, _ := newIdleLifecycleTestRouter(t, 1800)
	params, err := json.Marshal(map[string]any{
		"threadId": "child",
		"config":   map[string]any{"thread_unload_delay_secs": 0},
	})
	if err != nil {
		t.Fatal(err)
	}
	router.applyThreadUnloadDelayFromRequest(&Request{Method: MethodThreadFork, Params: params}, &ThreadForkResponse{Thread: &Thread{ID: "child"}})
	if delay, ok := router.threadUnloadDelayOverride("child"); !ok || delay != 0 {
		t.Fatalf("override = %v/%v, want 0", delay, ok)
	}
	if got := router.threadIdleUnloadDelay("child"); got != 0 {
		t.Fatalf("threadIdleUnloadDelay = %v, want the per-thread override", got)
	}
	router.markThreadUnloaded("child")
	if _, ok := router.threadUnloadDelayOverride("child"); ok {
		t.Fatal("cold unload retained the runtime override")
	}
}

func TestThreadUnloadDelayValidationAndPredictionForkLikeRust(t *testing.T) {
	for _, value := range []any{-1, 1.5, "2"} {
		params := &ThreadForkParams{ThreadID: "root", Config: map[string]any{"thread_unload_delay_secs": value}}
		if err := params.Validate(); err == nil {
			t.Fatalf("thread_unload_delay_secs=%#v was accepted", value)
		}
	}
	allowed := &ThreadForkParams{
		ThreadID:                   "root",
		Ephemeral:                  true,
		ExperimentalPredictionMode: true,
		Config:                     map[string]any{"thread_unload_delay_secs": 0},
	}
	if err := allowed.Validate(); err != nil {
		t.Fatalf("prediction fork with a delay override was rejected: %v", err)
	}
	rejected := &ThreadForkParams{
		ThreadID:                   "root",
		Ephemeral:                  true,
		ExperimentalPredictionMode: true,
		Config:                     map[string]any{"model": "other"},
	}
	if err := rejected.Validate(); err == nil {
		t.Fatal("prediction fork accepted an unrelated config override")
	}
}
