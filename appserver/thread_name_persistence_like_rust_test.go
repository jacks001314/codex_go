package appserver

import (
	"os"
	"testing"
	"time"

	"codex_go/rollout"
	"codex_go/session"
)

// Rust parity: #49785 (`thread_name_persistence`) names an empty *paginated*
// thread before its first turn, then resumes it before and after an app-server
// restart. Naming a paginated thread must persist the live rollout
// (`update_thread_metadata` persists the recorder before the SQLite write) so
// the named thread is durable; a legacy thread keeps the pre-existing
// unmaterialized behavior, and naming must not invent a user turn.
func TestRouterThreadSetNameMaterializesEmptyPaginatedThreadLikeRust(t *testing.T) {
	store := session.NewStore(t.TempDir())
	router := NewRouter(store)
	router.SetClock(func() time.Time { return fixedTime() })

	start := router.Handle(requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{
		CWD:         "D:/repo",
		HistoryMode: ThreadHistoryPaginated,
	}))
	if start.Error != nil {
		t.Fatalf("thread/start error: %+v", start.Error)
	}
	threadID := start.Result.(*ThreadStartResponse).Thread.ID
	if _, err := rollout.FindThreadPath(store.Root(), threadID, false); err == nil {
		t.Fatal("fresh paginated thread materialized before naming")
	}

	named := router.Handle(requestWithParams(t, IntID(2), MethodThreadNameSet, ThreadSetNameParams{
		ThreadID: threadID,
		Name:     "Scheduled task",
	}))
	if named.Error != nil {
		t.Fatalf("thread/name/set error: %+v", named.Error)
	}
	path, err := rollout.FindThreadPath(store.Root(), threadID, false)
	if err != nil {
		t.Fatalf("named paginated thread rollout path error: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("named paginated thread rollout missing at %q: %v", path, err)
	}

	// Run-now resumes immediately after naming, before any user turn exists.
	resume := router.Handle(requestWithParams(t, IntID(3), MethodThreadResume, ThreadResumeParams{
		ThreadID:     threadID,
		ExcludeTurns: true,
	}))
	if resume.Error != nil {
		t.Fatalf("resume after naming error: %+v", resume.Error)
	}
	resumed := resume.Result.(*ThreadResumeResponse).Thread
	if resumed.ID != threadID || resumed.Name == nil || *resumed.Name != "Scheduled task" {
		t.Fatalf("resume after naming thread = %+v", resumed)
	}
	if len(resumed.Turns) != 0 {
		t.Fatalf("resume after naming turns = %+v, want an empty turn history", resumed.Turns)
	}

	// Restart the app-server over the same codex home; the named empty thread
	// must still be resumable.
	if err := router.Close(); err != nil {
		t.Fatalf("router.Close() error = %v", err)
	}
	restarted := NewRouter(store)
	restarted.SetClock(func() time.Time { return fixedTime() })
	t.Cleanup(func() { _ = restarted.Close() })
	resumeAfterRestart := restarted.Handle(requestWithParams(t, IntID(4), MethodThreadResume, ThreadResumeParams{
		ThreadID:     threadID,
		ExcludeTurns: true,
	}))
	if resumeAfterRestart.Error != nil {
		t.Fatalf("resume after restart error: %+v", resumeAfterRestart.Error)
	}
	resumedAfterRestart := resumeAfterRestart.Result.(*ThreadResumeResponse).Thread
	if resumedAfterRestart.ID != threadID || resumedAfterRestart.Name == nil || *resumedAfterRestart.Name != "Scheduled task" {
		t.Fatalf("resume after restart thread = %+v", resumedAfterRestart)
	}
}
