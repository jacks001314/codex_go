package appserver

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codex_go/session"
	"codex_go/state"
)

// Rust #60466: when the diagnostic logs database never opened, thread/delete is
// rejected before destructive cleanup, so the saved rollout and the conversation
// rows survive and thread/read still returns the turns.
func TestThreadDeletePreservesSavedConversationWhenDiagnosticLogsUnavailableLikeRust(t *testing.T) {
	home := t.TempDir()
	blockedLogs := filepath.Join(home, "blocked-logs")
	if err := os.WriteFile(blockedLogs, []byte("not a directory"), 0o600); err != nil {
		t.Fatalf("write blocked logs path: %v", err)
	}
	sqliteConfig, err := state.SqliteConfigForCodexHome(home)
	if err != nil {
		t.Fatalf("SqliteConfigForCodexHome() error = %v", err)
	}
	runtime, err := state.InitStateRuntime(context.Background(), sqliteConfig.WithLocalLogsHome(blockedLogs), "openai")
	if err != nil {
		t.Fatalf("InitStateRuntime() error = %v, want a runtime without diagnostic logs", err)
	}
	t.Cleanup(func() { _ = runtime.Close() })
	if runtime.DiagnosticLogsError() == nil {
		t.Fatal("DiagnosticLogsError() = nil, want the startup failure")
	}

	store := session.NewStore(home)
	router := NewRouter(store)
	router.SetStateRuntime(runtime)
	t.Cleanup(func() { _ = router.Close() })

	started := router.Handle(requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{
		CWD:    t.TempDir(),
		Prompt: "keep me",
	}))
	if started.Error != nil {
		t.Fatalf("thread/start error: %+v", started.Error)
	}
	threadID := started.Result.(*ThreadStartResponse).Thread.ID
	materializeThreadRolloutForTest(t, router, store, threadID)
	record, err := store.Read(session.ThreadID(threadID), true, true)
	if err != nil {
		t.Fatalf("read saved thread: %v", err)
	}
	rolloutPath := router.threadRolloutPath(record)
	before, err := os.ReadFile(rolloutPath)
	if err != nil {
		t.Fatalf("read rollout %q: %v", rolloutPath, err)
	}

	deleted := router.Handle(requestWithParams(t, IntID(2), MethodThreadDelete, ThreadDeleteParams{ThreadID: threadID}))
	if deleted.Error == nil {
		t.Fatal("thread/delete succeeded, want the diagnostic database failure")
	}
	if deleted.Error.Code != JSONRPCInternalErrorCode {
		t.Fatalf("thread/delete code = %d, want %d", deleted.Error.Code, JSONRPCInternalErrorCode)
	}
	if !strings.Contains(deleted.Error.Message, blockedLogs) {
		t.Fatalf("thread/delete message = %q, want the original logs path %s", deleted.Error.Message, blockedLogs)
	}

	after, err := os.ReadFile(rolloutPath)
	if err != nil {
		t.Fatalf("rollout %q removed by a failed delete: %v", rolloutPath, err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("rollout contents changed during a failed delete")
	}
	read := router.Handle(requestWithParams(t, IntID(3), MethodThreadRead, ThreadReadParams{ThreadID: threadID, IncludeTurns: true}))
	if read.Error != nil {
		t.Fatalf("thread/read after failed delete error: %+v", read.Error)
	}
}
