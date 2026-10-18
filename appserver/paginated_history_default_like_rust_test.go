package appserver

import (
	"strings"
	"testing"

	"codex_go/session"
)

// TestRuntimeRouterDefaultsLocalThreadsToPaginatedHistoryLikeRust mirrors
// Rust #47900: a local thread/start without historyMode uses paginated
// history, while an explicit legacy selection is preserved.
func TestRuntimeRouterDefaultsLocalThreadsToPaginatedHistoryLikeRust(t *testing.T) {
	home := t.TempDir()
	store := session.NewStore(home)
	router := NewDefaultRuntimeRouter(store, home)
	t.Cleanup(func() { _ = router.Close() })

	start := func(id int64, mode ThreadHistoryMode) *ThreadStartResponse {
		t.Helper()
		response := router.Handle(requestWithParams(t, IntID(id), MethodThreadStart, ThreadStartParams{
			CWD:         t.TempDir(),
			HistoryMode: mode,
		}))
		if response.Error != nil {
			t.Fatalf("thread/start error = %+v", response.Error)
		}
		return response.Result.(*ThreadStartResponse)
	}

	defaulted := start(1, "")
	record, err := store.Read(session.ThreadID(defaulted.Thread.ID), true, false)
	if err != nil {
		t.Fatalf("Read(default) error = %v", err)
	}
	if record.Metadata.HistoryMode != string(ThreadHistoryPaginated) {
		t.Fatalf("default history mode = %q, want paginated", record.Metadata.HistoryMode)
	}
	if defaulted.Thread.HistoryMode != ThreadHistoryPaginated {
		t.Fatalf("default response history mode = %q, want paginated", defaulted.Thread.HistoryMode)
	}

	legacy := start(2, ThreadHistoryLegacy)
	record, err = store.Read(session.ThreadID(legacy.Thread.ID), true, false)
	if err != nil {
		t.Fatalf("Read(legacy) error = %v", err)
	}
	if record.Metadata.HistoryMode != string(ThreadHistoryLegacy) {
		t.Fatalf("explicit legacy history mode = %q, want legacy", record.Metadata.HistoryMode)
	}
}

// TestRuntimeRouterRejectsEphemeralThreadRevertLikeRust mirrors Rust #47900:
// ephemeral threads keep their in-memory context and cannot be reverted.
func TestRuntimeRouterRejectsEphemeralThreadRevertLikeRust(t *testing.T) {
	home := t.TempDir()
	router := NewDefaultRuntimeRouter(session.NewStore(home), home)
	t.Cleanup(func() { _ = router.Close() })

	start := router.Handle(requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{
		CWD:         t.TempDir(),
		Prompt:      "keep this context",
		Ephemeral:   true,
		HistoryMode: ThreadHistoryPaginated,
	}))
	if start.Error != nil {
		t.Fatalf("ephemeral thread/start error = %+v", start.Error)
	}
	threadID := start.Result.(*ThreadStartResponse).Thread.ID

	revert := router.Handle(requestWithParams(t, IntID(2), MethodThreadRevert, ThreadRevertParams{
		ThreadID:     threadID,
		BeforeTurnID: "turn-1",
	}))
	if revert.Error == nil || !strings.Contains(revert.Error.Message, "ephemeral threads do not support thread/revert") {
		t.Fatalf("thread/revert error = %+v, want ephemeral rejection", revert.Error)
	}
	read := router.Handle(requestWithParams(t, IntID(3), MethodThreadRead, ThreadReadParams{
		ThreadID:     threadID,
		IncludeTurns: false,
	}))
	if read.Error != nil {
		t.Fatalf("thread/read error = %+v", read.Error)
	}
	thread := read.Result.(*ThreadReadResponse).Thread
	if thread == nil || !thread.Ephemeral {
		t.Fatalf("thread after rejected revert = %+v, want retained ephemeral thread", thread)
	}
}
