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

// TestPaginatedFullHistoryRequestsEmitDeprecationNoticesLikeRust covers Rust
// #40676: reads, resumes, and forks that request full history for a paginated
// thread emit a connection-scoped deprecation notice, while metadata-only
// requests stay silent.
func TestPaginatedFullHistoryRequestsEmitDeprecationNoticesLikeRust(t *testing.T) {
	home := t.TempDir()
	store := session.NewStore(home)
	router := NewDefaultRuntimeRouter(store, home)
	t.Cleanup(func() { _ = router.Close() })
	sink := &targetedNotificationTestSink{}
	router.SetNotificationSink(sink)
	initializeAttestationTestConnection(t, router, "conn-a", false)

	threadID := session.ThreadID("thread-paginated-notice")
	if err := store.Create(&session.Record{
		ID:        threadID,
		SessionID: string(threadID),
		Metadata:  session.Metadata{CWD: t.TempDir(), HistoryMode: string(ThreadHistoryPaginated)},
	}); err != nil {
		t.Fatalf("create thread error = %v", err)
	}
	if _, err := store.AppendItem(threadID, session.Item{
		ID:       "item-1",
		Type:     "user_message",
		Text:     "hello",
		Metadata: map[string]any{"turnId": "turn-1"},
	}); err != nil {
		t.Fatalf("append item error = %v", err)
	}

	readRequest := requestWithParams(t, IntID(2), MethodThreadRead, ThreadReadParams{
		ThreadID:     string(threadID),
		IncludeTurns: true,
	})
	readRequest.ConnectionID = "conn-a"
	read := router.Handle(readRequest)
	if read.Error != nil {
		t.Fatalf("thread/read error = %+v", read.Error)
	}
	if count := countDeprecationNoticesForConnection(sink, "conn-a"); count != 1 {
		t.Fatalf("full read notices = %d, want 1", count)
	}

	metadataRead := requestWithParams(t, IntID(3), MethodThreadRead, ThreadReadParams{ThreadID: string(threadID)})
	metadataRead.ConnectionID = "conn-a"
	if response := router.Handle(metadataRead); response.Error != nil {
		t.Fatalf("metadata read error = %+v", response.Error)
	}
	if count := countDeprecationNoticesForConnection(sink, "conn-a"); count != 1 {
		t.Fatalf("metadata read emitted a notice: count = %d", count)
	}

	resume := requestWithParams(t, IntID(4), MethodThreadResume, ThreadResumeParams{ThreadID: string(threadID)})
	resume.ConnectionID = "conn-a"
	if response := router.Handle(resume); response.Error != nil {
		t.Fatalf("full resume error = %+v", response.Error)
	}
	if count := countDeprecationNoticesForConnection(sink, "conn-a"); count != 2 {
		t.Fatalf("full resume notices = %d, want 2", count)
	}

	resumeMetadata := requestWithParams(t, IntID(5), MethodThreadResume, ThreadResumeParams{
		ThreadID:     string(threadID),
		ExcludeTurns: true,
	})
	resumeMetadata.ConnectionID = "conn-a"
	if response := router.Handle(resumeMetadata); response.Error != nil {
		t.Fatalf("metadata resume error = %+v", response.Error)
	}
	if count := countDeprecationNoticesForConnection(sink, "conn-a"); count != 2 {
		t.Fatalf("metadata resume emitted a notice: count = %d", count)
	}

	fork := requestWithParams(t, IntID(6), MethodThreadFork, ThreadForkParams{ThreadID: string(threadID)})
	fork.ConnectionID = "conn-a"
	if response := router.Handle(fork); response.Error != nil {
		t.Fatalf("full fork error = %+v", response.Error)
	}
	if count := countDeprecationNoticesForConnection(sink, "conn-a"); count != 3 {
		t.Fatalf("full fork notices = %d, want 3", count)
	}

	forkMetadata := requestWithParams(t, IntID(7), MethodThreadFork, ThreadForkParams{
		ThreadID:     string(threadID),
		ExcludeTurns: true,
	})
	forkMetadata.ConnectionID = "conn-a"
	if response := router.Handle(forkMetadata); response.Error != nil {
		t.Fatalf("metadata fork error = %+v", response.Error)
	}
	if count := countDeprecationNoticesForConnection(sink, "conn-a"); count != 3 {
		t.Fatalf("metadata fork emitted a notice: count = %d", count)
	}
}
