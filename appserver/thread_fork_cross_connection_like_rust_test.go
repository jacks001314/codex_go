package appserver

import (
	"strings"
	"testing"
	"time"

	"codex_go/session"
)

// A daemon serves each RPC connection with its own Router, but the thread
// writer belongs to the server process. Forking a live source from another
// connection must use that process-owned writer instead of reporting a
// cross-process contention error.
func TestThreadForkUsesLiveWriterFromAnotherConnectionLikeRust(t *testing.T) {
	root := t.TempDir()
	owner := NewRouter(session.NewStore(root))
	record := crossConnectionForkRecord("thread-cross-connection-fork")
	if err := owner.store.Create(record); err != nil {
		t.Fatalf("Create(source) error = %v", err)
	}
	if err := owner.retainLiveThread(record); err != nil {
		t.Fatalf("retainLiveThread(source) error = %v", err)
	}
	t.Cleanup(func() { _ = owner.Close() })

	// A separate Store and ThreadManager model the next WebSocket connection
	// served by the daemon.
	forker := NewRouter(session.NewStore(root))
	t.Cleanup(func() { _ = forker.Close() })
	response := forker.Handle(requestWithParams(t, IntID(1), MethodThreadFork, ThreadForkParams{
		ThreadID:     string(record.ID),
		Ephemeral:    true,
		ExcludeTurns: true,
	}))
	if response.Error != nil {
		t.Fatalf("thread/fork error = %+v", response.Error)
	}
	forked := response.Result.(*ThreadForkResponse)
	if forked.Thread == nil || forked.Thread.ID == "" || forked.Thread.ForkedFromID == nil || *forked.Thread.ForkedFromID != string(record.ID) {
		t.Fatalf("thread/fork response = %+v", forked.Thread)
	}

	// Closing the owning connection must remove the process-owned entry. A
	// writer without a registered LiveThread then models another Codex process
	// and keeps the ordinary active-writer protection.
	if err := owner.Close(); err != nil {
		t.Fatalf("owner.Close() error = %v", err)
	}
	foreignWriter, err := session.NewStore(root).AcquireWriter(record.ID)
	if err != nil {
		t.Fatalf("AcquireWriter() error = %v", err)
	}
	defer func() { _ = foreignWriter.Close() }()
	blocked := forker.Handle(requestWithParams(t, IntID(2), MethodThreadFork, ThreadForkParams{
		ThreadID:     string(record.ID),
		Ephemeral:    true,
		ExcludeTurns: true,
	}))
	if blocked.Error == nil || !strings.Contains(blocked.Error.Message, "already has an active writer") {
		t.Fatalf("thread/fork with a foreign writer = %+v, want active-writer conflict", blocked)
	}
}

// /side uses the runtime's ephemeral-fork fast path, so keep that branch under
// the same cross-connection regression test.
func TestEphemeralThreadForkUsesLiveWriterFromAnotherConnectionLikeRust(t *testing.T) {
	root := t.TempDir()
	owner := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(session.NewStore(root)),
		ThreadStatus: NewThreadStatusManager(),
	})
	record := crossConnectionForkRecord("thread-cross-connection-ephemeral-fork")
	if err := owner.services.ThreadRouter.store.Create(record); err != nil {
		t.Fatalf("Create(source) error = %v", err)
	}
	if err := owner.services.ThreadRouter.retainLiveThread(record); err != nil {
		t.Fatalf("retainLiveThread(source) error = %v", err)
	}
	t.Cleanup(func() { _ = owner.Close() })

	forker := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(session.NewStore(root)),
		ThreadStatus: NewThreadStatusManager(),
	})
	t.Cleanup(func() { _ = forker.Close() })
	response := forker.Handle(requestWithParams(t, IntID(1), MethodThreadFork, ThreadForkParams{
		ThreadID:     string(record.ID),
		Ephemeral:    true,
		ExcludeTurns: true,
	}))
	if response.Error != nil {
		t.Fatalf("ephemeral thread/fork error = %+v", response.Error)
	}
	forked := response.Result.(*ThreadForkResponse)
	if forked.Thread == nil || !forked.Thread.Ephemeral || forked.Thread.ForkedFromID == nil || *forked.Thread.ForkedFromID != string(record.ID) {
		t.Fatalf("ephemeral thread/fork response = %+v", forked.Thread)
	}
}

func crossConnectionForkRecord(threadID string) *session.Record {
	now := time.Now().UTC()
	return &session.Record{
		ID:        session.ThreadID(threadID),
		SessionID: threadID,
		CreatedAt: now,
		UpdatedAt: now,
		RecencyAt: now,
		Metadata:  session.Metadata{HistoryMode: string(ThreadHistoryPaginated)},
	}
}
