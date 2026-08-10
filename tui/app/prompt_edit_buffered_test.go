package app

import (
	"context"
	"testing"

	"codex_go/appserver"
	"codex_go/tui/chatwidget"
)

func TestTurnsIncludingBufferedReconstructsLiveTurnsLikeRust(t *testing.T) {
	store := NewThreadEventStore(64)
	store.SetTurns([]appserver.Turn{
		{ID: "turn-1", Items: []appserver.ThreadItem{
			{ID: "item-1", Type: "message", Role: "user", Text: "hello"},
		}, Status: appserver.TurnStatusCompleted},
	})
	// A new live turn exists only in the replay buffer.
	store.EnqueueNotification(ServerEvent{Name: ServerNotificationTurnStarted, TurnID: "turn-2", Turn: &appserver.Turn{ID: "turn-2", Status: appserver.TurnStatusInProgress, StartedAt: int64Ptr(1000)}})
	store.EnqueueNotification(ServerEvent{
		Name:   ServerNotificationItemCompleted,
		TurnID: "turn-2",
		Item:   &appserver.ThreadItem{ID: "item-2", Type: "message", Role: "user", Text: "edit me"},
	})
	store.EnqueueNotification(ServerEvent{Name: ServerNotificationTurnCompleted, TurnID: "turn-2", Turn: &appserver.Turn{ID: "turn-2", Status: appserver.TurnStatusCompleted, StartedAt: int64Ptr(1000), CompletedAt: int64Ptr(2000), DurationMS: int64Ptr(1000)}})

	turns := store.TurnsIncludingBuffered()
	if len(turns) != 2 {
		t.Fatalf("turns = %+v, want 2 (snapshot + buffered)", turns)
	}
	buffered := turns[1]
	if buffered.ID != "turn-2" {
		t.Fatalf("buffered turn id = %q, want turn-2", buffered.ID)
	}
	if buffered.Status != appserver.TurnStatusCompleted {
		t.Fatalf("buffered turn status = %q, want completed", buffered.Status)
	}
	if buffered.CompletedAt == nil || *buffered.CompletedAt != 2000 {
		t.Fatalf("buffered turn completedAt = %v, want 2000", buffered.CompletedAt)
	}
	if len(buffered.Items) != 1 || buffered.Items[0].ID != "item-2" {
		t.Fatalf("buffered items = %+v, want item-2", buffered.Items)
	}
	// Snapshot turn is untouched and not duplicated.
	if turns[0].ID != "turn-1" || len(turns[0].Items) != 1 {
		t.Fatalf("snapshot turn = %+v", turns[0])
	}
}

func TestTurnsIncludingBufferedAvoidsDuplicates(t *testing.T) {
	store := NewThreadEventStore(64)
	store.SetTurns([]appserver.Turn{
		{ID: "turn-1", Items: []appserver.ThreadItem{
			{ID: "item-1", Type: "message", Role: "user", Text: "hello"},
		}, Status: appserver.TurnStatusInProgress},
	})
	// The buffered turn already exists in the snapshot: no duplicate.
	store.EnqueueNotification(ServerEvent{Name: ServerNotificationTurnStarted, TurnID: "turn-1", Turn: &appserver.Turn{ID: "turn-1"}})
	// The item is already present: no duplicate item.
	store.EnqueueNotification(ServerEvent{Name: ServerNotificationItemCompleted, TurnID: "turn-1", Item: &appserver.ThreadItem{ID: "item-1", Type: "message", Role: "user"}})

	turns := store.TurnsIncludingBuffered()
	if len(turns) != 1 {
		t.Fatalf("turns = %+v, want 1 (no duplicates)", turns)
	}
	if len(turns[0].Items) != 1 {
		t.Fatalf("items = %+v, want 1 (no duplicate items)", turns[0].Items)
	}
}

func TestApplyPromptEditWithStoreFindsBufferedPrompt(t *testing.T) {
	forkedFrom := "source"
	client := &promptEditClient{forked: &appserver.ThreadForkResponse{Thread: &appserver.Thread{ID: "forked", ForkedFromID: &forkedFrom, CWD: "/repo"}}}
	store := NewThreadEventStore(64)
	store.SetTurns([]appserver.Turn{
		{ID: "turn-1", Items: []appserver.ThreadItem{
			{ID: "item-1", Type: "message", Role: "user", Text: "first"},
		}, Status: appserver.TurnStatusCompleted},
	})
	// The selected prompt lives in the replay buffer only.
	store.EnqueueNotification(ServerEvent{Name: ServerNotificationTurnStarted, TurnID: "turn-2", Turn: &appserver.Turn{ID: "turn-2", Status: appserver.TurnStatusInProgress}})
	store.EnqueueNotification(ServerEvent{Name: ServerNotificationItemCompleted, TurnID: "turn-2", Item: &appserver.ThreadItem{ID: "item-2", Type: "message", Role: "user", Text: "second"}})

	source := ThreadSessionState{ThreadID: "source", CWD: "/repo"}
	result := ApplyPromptEditWithStore(context.Background(), client, source, store, PromptEditSelection{
		ThreadID:    "source",
		UserOrdinal: 1,
		Prompt:      chatwidget.ThreadComposerState{},
	})
	if result.ErrorMessage != "" {
		t.Fatalf("ErrorMessage = %q, want empty (buffered prompt found)", result.ErrorMessage)
	}
	if !result.Branched {
		t.Fatalf("Branched = false, want true")
	}
	if client.forkParams.BeforeTurnID != "turn-2" {
		t.Fatalf("forkParams.BeforeTurnID = %q, want turn-2", client.forkParams.BeforeTurnID)
	}
}

func int64Ptr(value int64) *int64 {
	return &value
}
