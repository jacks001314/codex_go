package appserver

import (
	"encoding/json"
	"testing"
	"time"

	"codex_go/session"
)

func historyInitializationForRecord(t *testing.T, router *Router, threadID string) session.HistoryInitialization {
	t.Helper()
	return router.historyInitializationForThread(session.ThreadID(threadID))
}

// Rust #52325: runtime history provenance distinguishes a new runtime, a cold
// resume, and forks from loaded versus unloaded source threads.
func TestHistoryInitializationClassificationLikeRust(t *testing.T) {
	store := session.NewStore(t.TempDir())
	router := NewRouter(store)
	started := router.Handle(requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{
		CWD:    t.TempDir(),
		Prompt: "initial history",
	}))
	if started.Error != nil {
		t.Fatalf("thread/start error = %+v", started.Error)
	}
	threadID := started.Result.(*ThreadStartResponse).Thread.ID
	if got := historyInitializationForRecord(t, router, threadID); got != session.HistoryInitializationNew {
		t.Fatalf("new thread initialization = %q", got)
	}
	if _, err := router.appendThreadItems(session.ThreadID(threadID), []session.Item{{
		ID: "history-marker", Type: "message", Role: "user", Text: "initial history", CreatedAt: time.Now().UTC(),
	}}); err != nil {
		t.Fatal(err)
	}
	record, err := router.store.Load(session.ThreadID(threadID))
	if err != nil {
		t.Fatal(err)
	}
	if err := router.createThreadRollout(record, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}

	// Rejoining the loaded runtime preserves the original classification.
	resumedLive := router.Handle(requestWithParams(t, IntID(2), MethodThreadResume, ThreadResumeParams{ThreadID: threadID}))
	if resumedLive.Error != nil {
		t.Fatalf("warm resume error = %+v", resumedLive.Error)
	}
	if got := historyInitializationForRecord(t, router, threadID); got != session.HistoryInitializationNew {
		t.Fatalf("warm resume initialization = %q", got)
	}
	warmFork := router.Handle(requestWithParams(t, IntID(3), MethodThreadFork, ThreadForkParams{ThreadID: threadID}))
	if warmFork.Error != nil {
		t.Fatalf("warm fork error = %+v", warmFork.Error)
	}
	warmForkID := warmFork.Result.(*ThreadForkResponse).Thread.ID
	if got := historyInitializationForRecord(t, router, warmForkID); got != session.HistoryInitializationWarmFork {
		t.Fatalf("warm fork initialization = %q", got)
	}

	cold := NewRouter(store)
	coldResume := cold.Handle(requestWithParams(t, IntID(4), MethodThreadResume, ThreadResumeParams{ThreadID: threadID}))
	if coldResume.Error != nil {
		t.Fatalf("cold resume error = %+v", coldResume.Error)
	}
	if got := historyInitializationForRecord(t, cold, threadID); got != session.HistoryInitializationColdResume {
		t.Fatalf("cold resume initialization = %q", got)
	}
	coldFork := cold.Handle(requestWithParams(t, IntID(5), MethodThreadFork, ThreadForkParams{ThreadID: threadID}))
	if coldFork.Error != nil {
		t.Fatalf("cold fork error = %+v", coldFork.Error)
	}
	coldForkID := coldFork.Result.(*ThreadForkResponse).Thread.ID
	if got := historyInitializationForRecord(t, cold, coldForkID); got != session.HistoryInitializationWarmFork {
		// The source was loaded by the preceding cold resume.
		t.Fatalf("loaded-source fork initialization = %q", got)
	}

	third := NewRouter(store)
	coldFork2 := third.Handle(requestWithParams(t, IntID(6), MethodThreadFork, ThreadForkParams{ThreadID: threadID}))
	if coldFork2.Error != nil {
		t.Fatalf("cold fork error = %+v", coldFork2.Error)
	}
	coldFork2ID := coldFork2.Result.(*ThreadForkResponse).Thread.ID
	if got := historyInitializationForRecord(t, third, coldFork2ID); got != session.HistoryInitializationColdFork {
		t.Fatalf("cold fork initialization = %q", got)
	}

	suppliedRouter := NewRouter(session.NewStore(t.TempDir()))
	supplied := suppliedRouter.Handle(requestWithParams(t, IntID(7), MethodThreadResume, ThreadResumeParams{
		ThreadID:   "supplied",
		HistorySet: true,
		History:    []ThreadResumeHistoryItem{ThreadResumeHistoryItem(json.RawMessage(`{"type":"message","role":"user","content":[{"type":"input_text","text":"supplied"}]}`))},
	}))
	if supplied.Error != nil {
		t.Fatalf("supplied-history resume error = %+v", supplied.Error)
	}
	suppliedID := supplied.Result.(*ThreadResumeResponse).Thread.ID
	if got := historyInitializationForRecord(t, suppliedRouter, suppliedID); got != session.HistoryInitializationSuppliedHistory {
		t.Fatalf("supplied history initialization = %q", got)
	}
}
