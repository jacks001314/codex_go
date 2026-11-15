package appserver

import (
	"testing"
	"time"

	"codex_go/session"
)

func liveForkParentRecord(threadID string, now time.Time) *session.Record {
	return &session.Record{
		ID:        session.ThreadID(threadID),
		SessionID: threadID,
		CreatedAt: now,
		UpdatedAt: now,
		RecencyAt: now,
		Metadata:  session.Metadata{HistoryMode: string(ThreadHistoryPaginated)},
		Items: []session.Item{
			{ID: "user-1", Type: "message", Role: "user", Text: "parent question", CreatedAt: now, Metadata: map[string]any{"turnId": "turn-1"}},
			{ID: "fco-durable", Type: "tool_output", CallID: "call-1", Text: "clean", CreatedAt: now, Metadata: map[string]any{"turnId": "turn-1"}},
		},
	}
}

func appendLiveForkItems(t *testing.T, store *session.Store, threadID string) *session.LiveThreadItems {
	t.Helper()
	live := session.OpenLiveThreadItems(store, session.ThreadID(threadID), "turn-2")
	if live == nil {
		t.Fatal("OpenLiveThreadItems() = nil")
	}
	live.Append(
		session.Item{ID: "assistant-2", Type: "agent_message", Role: "assistant", Text: "Inspecting the repo.", Metadata: map[string]any{"turnId": "turn-2"}},
		session.Item{ID: "tool-call-2", Type: "function_call", Name: "shell", CallID: "call-2", Text: "git status", Metadata: map[string]any{"turnId": "turn-2"}},
		session.Item{ID: "fco-live", Type: "tool_output", CallID: "call-2", Text: "clean", Metadata: map[string]any{"turnId": "turn-2"}},
		// The durable history already has this output; forks must not repeat it.
		session.Item{ID: "fco-redundant", Type: "tool_output", CallID: "call-1", Text: "clean", Metadata: map[string]any{"turnId": "turn-2"}},
	)
	return live
}

func assertLiveForkItems(t *testing.T, record *session.Record) {
	t.Helper()
	if record == nil {
		t.Fatal("fork record = nil")
	}
	wantIDs := []string{"user-1", "fco-durable", "assistant-2", "tool-call-2", "fco-live"}
	if len(record.Items) != len(wantIDs) {
		t.Fatalf("fork items = %#v, want %v", record.Items, wantIDs)
	}
	for i, want := range wantIDs {
		if record.Items[i].ID != want {
			t.Fatalf("fork item %d = %#v, want id %q", i, record.Items[i], want)
		}
	}
}

func TestThreadForkReadsLiveThreadItemsLikeRust(t *testing.T) {
	store := session.NewStore(t.TempDir())
	router := NewRouter(store)
	t.Cleanup(func() { _ = router.Close() })
	now := time.Now().UTC()
	parent := liveForkParentRecord("thread-live-fork-parent", now)
	if err := store.Create(parent); err != nil {
		t.Fatalf("Create(parent) error = %v", err)
	}
	live := appendLiveForkItems(t, store, string(parent.ID))
	defer live.Close()

	response := router.Handle(requestWithParams(t, IntID(1), MethodThreadFork, ThreadForkParams{ThreadID: string(parent.ID)}))
	if response.Error != nil {
		t.Fatalf("thread/fork error = %+v", response.Error)
	}
	forked := response.Result.(*ThreadForkResponse)
	record, err := store.Read(session.ThreadID(forked.Thread.ID), true, true)
	if err != nil {
		t.Fatalf("Read(fork) error = %v", err)
	}
	assertLiveForkItems(t, record)
}

func TestRuntimeThreadForkReadsLiveThreadItemsLikeRust(t *testing.T) {
	root := t.TempDir()
	store := session.NewStore(root)
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(session.NewStore(root)),
		ThreadStatus: NewThreadStatusManager(),
	})
	t.Cleanup(func() { _ = router.Close() })
	now := time.Now().UTC()
	parent := liveForkParentRecord("thread-live-runtime-fork-parent", now)
	if err := store.Create(parent); err != nil {
		t.Fatalf("Create(parent) error = %v", err)
	}
	live := appendLiveForkItems(t, store, string(parent.ID))
	defer live.Close()

	response := router.Handle(requestWithParams(t, IntID(1), MethodThreadFork, ThreadForkParams{ThreadID: string(parent.ID)}))
	if response.Error != nil {
		t.Fatalf("thread/fork error = %+v", response.Error)
	}
	forked := response.Result.(*ThreadForkResponse)
	record, err := store.Read(session.ThreadID(forked.Thread.ID), true, true)
	if err != nil {
		t.Fatalf("Read(fork) error = %v", err)
	}
	assertLiveForkItems(t, record)
}
