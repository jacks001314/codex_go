package appserver

import (
	"testing"
	"time"

	"codex_go/session"
	"codex_go/turn"
)

// Mirrors Rust #61014: a recovery turn can own the core turn slot before
// TurnStarted is published. Resume overrides and idle eviction must observe
// that reservation instead of treating the thread as idle.
func TestResumeAndIdleEvictionPreserveReservedRecoveryTurnLikeRust(t *testing.T) {
	store := session.NewStore(t.TempDir())
	now := time.Now().UTC()
	cwd := t.TempDir()
	parentID := "thread-recovery-parent"
	threadID := "thread-recovery-child"
	for _, record := range []*session.Record{
		{
			ID: session.ThreadID(parentID), SessionID: parentID,
			CreatedAt: now, UpdatedAt: now, RecencyAt: now,
			Metadata: session.Metadata{CWD: cwd, HistoryMode: "legacy"},
		},
		{
			ID: session.ThreadID(threadID), SessionID: threadID,
			ParentThreadID: session.ThreadID(parentID),
			CreatedAt:      now, UpdatedAt: now, RecencyAt: now,
			Metadata: session.Metadata{CWD: cwd, HistoryMode: "legacy"},
		},
	} {
		if err := store.Create(record); err != nil {
			t.Fatalf("Create(%s) error = %v", record.ID, err)
		}
	}
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		Turns:        turn.NewTurnService(),
		ThreadStatus: NewThreadStatusManager(),
	})
	t.Cleanup(func() { _ = router.Close() })

	loaded := router.Handle(requestWithParams(t, IntID(1), MethodThreadResume, ThreadResumeParams{ThreadID: threadID, ExcludeTurns: true}))
	if loaded.Error != nil {
		t.Fatalf("initial resume error = %+v", loaded.Error)
	}
	if !router.idleThreadEvictable(threadID) {
		t.Fatalf("thread %s is not evictable, so the idle lifecycle cannot be exercised", threadID)
	}

	// The recovery turn reserves the core slot; TurnStarted has not been
	// published, so thread status still reads idle.
	if err := router.threads.ReserveTurn(threadID); err != nil {
		t.Fatalf("ReserveTurn() error = %v", err)
	}
	if router.requireThreadStatus().LoadedStatusForThread(threadID).Type != IdleStatus().Type {
		t.Fatalf("loaded status = %#v, want idle before TurnStarted", router.requireThreadStatus().LoadedStatusForThread(threadID))
	}

	router.setThreadUnloadDelayOverride(threadID, 20*time.Millisecond, true)
	router.scheduleThreadIdleUnload(threadID)
	time.Sleep(150 * time.Millisecond)
	if !router.threadIsLoaded(threadID) {
		t.Fatal("idle lifecycle evicted a thread whose turn slot was already reserved")
	}
	if router.threads.ActiveTurn(threadID) == nil {
		t.Fatal("idle lifecycle released the reserved turn slot")
	}

	model := "gpt-recovery"
	resumed := router.Handle(requestWithParams(t, IntID(2), MethodThreadResume, ThreadResumeParams{
		ThreadID: threadID, ExcludeTurns: true, Model: &model,
	}))
	if resumed.Error != nil {
		t.Fatalf("resume with overrides error = %+v", resumed.Error)
	}
	if router.threads.ActiveTurn(threadID) == nil {
		t.Fatal("resume overrides released the recovery turn slot")
	}
	if !router.threadIsLoaded(threadID) {
		t.Fatal("resume overrides unloaded the thread that owns the recovery turn")
	}
}
