package app

import (
	"testing"

	"codex_go/internal/appserver"
)

func TestThreadEventStoreBuffersAndEvictsPendingRequestsMatchRust(t *testing.T) {
	store := NewThreadEventStore(2)
	first := ServerRequest{ID: "req-1", Kind: ServerRequestCommandExecutionApproval, TurnID: "turn-1", ItemID: "exec-1"}
	second := ServerRequest{ID: "req-2", Kind: ServerRequestFileChangeApproval, TurnID: "turn-1", ItemID: "patch-1"}
	store.EnqueueRequest(first)
	store.EnqueueRequest(second)
	if !store.HasPendingThreadApprovals() {
		t.Fatal("expected pending approvals after two requests")
	}

	store.EnqueueNotification(ServerEvent{Name: ServerNotificationWarning})
	if len(store.Buffer) != 2 {
		t.Fatalf("buffer len = %d, want 2", len(store.Buffer))
	}
	requests := store.PendingReplayRequests()
	if len(requests) != 1 || requests[0].ID != "req-2" {
		t.Fatalf("PendingReplayRequests after eviction = %#v, want req-2 only", requests)
	}
	snapshot := store.Snapshot()
	if len(snapshot.Events) != 2 || snapshot.Events[0].Request == nil || snapshot.Events[0].Request.ID != "req-2" {
		t.Fatalf("Snapshot() = %#v", snapshot.Events)
	}
}

func TestThreadEventStoreResolvedNotificationFiltersSnapshotMatchRust(t *testing.T) {
	store := NewThreadEventStore(8)
	store.EnqueueRequest(ServerRequest{ID: "req-1", Kind: ServerRequestUserInput, TurnID: "turn-1", ItemID: "call-1"})
	store.EnqueueRequest(ServerRequest{ID: "req-2", Kind: ServerRequestUserInput, TurnID: "turn-1", ItemID: "call-2"})
	store.EnqueueNotification(ServerEvent{Name: ServerNotificationServerRequestResolved, RequestID: "req-1"})

	requests := store.PendingReplayRequests()
	if len(requests) != 1 || requests[0].ID != "req-2" {
		t.Fatalf("PendingReplayRequests after resolve = %#v, want req-2 only", requests)
	}
	snapshot := store.Snapshot()
	if len(snapshot.Events) != 2 {
		t.Fatalf("snapshot events = %#v, want resolved request filtered plus req-2 and notification", snapshot.Events)
	}
	if snapshot.Events[0].Request == nil || snapshot.Events[0].Request.ID != "req-2" {
		t.Fatalf("snapshot first event = %#v, want req-2", snapshot.Events[0])
	}
	if snapshot.Events[1].Notification == nil || snapshot.Events[1].Notification.RequestID != "req-1" {
		t.Fatalf("snapshot second event = %#v, want resolve notification", snapshot.Events[1])
	}
}

func TestThreadEventStoreSideParentPendingStatusMatchRust(t *testing.T) {
	store := NewThreadEventStore(4)
	if status, ok := store.SideParentPendingStatus(); ok || status != "" {
		t.Fatalf("empty side parent status = %q/%v, want none", status, ok)
	}
	store.EnqueueRequest(ServerRequest{ID: "req-input", Kind: ServerRequestUserInput, TurnID: "turn-1", ItemID: "call-1"})
	if status, ok := store.SideParentPendingStatus(); !ok || status != SideParentStatusNeedsInput {
		t.Fatalf("input side parent status = %q/%v", status, ok)
	}
	store.EnqueueRequest(ServerRequest{ID: "req-approval", Kind: ServerRequestCommandExecutionApproval, TurnID: "turn-1", ItemID: "exec-1"})
	if status, ok := store.SideParentPendingStatus(); !ok || status != SideParentStatusNeedsInput {
		t.Fatalf("input should remain priority over approval side parent status = %q/%v", status, ok)
	}
	store.EnqueueNotification(ServerEvent{Name: ServerNotificationTurnCompleted, TurnID: "turn-1"})
	if status, ok := store.SideParentPendingStatus(); ok || status != "" {
		t.Fatalf("completed side parent status = %q/%v, want none", status, ok)
	}
}

func TestThreadEventStoreSnapshotClonesEvents(t *testing.T) {
	store := NewThreadEventStore(4)
	request := ServerRequest{ID: "req-1", Kind: ServerRequestUserInput, TurnID: "turn-1", ItemID: "call-1"}
	store.EnqueueRequest(request)

	snapshot := store.Snapshot()
	snapshot.Events[0].Request.ID = "mutated"
	if store.Buffer[0].Request.ID != "req-1" {
		t.Fatalf("snapshot mutation leaked into store: %#v", store.Buffer[0].Request)
	}
}

func TestThreadEventStoreSessionTurnsAndInputSnapshotMatchRust(t *testing.T) {
	serviceTier := "priority"
	session := ThreadSessionState{
		ThreadID:    "thread-1",
		Model:       "gpt-test",
		ServiceTier: &serviceTier,
	}
	store := NewThreadEventStoreWithSession(4, session, []appserver.Turn{
		{ID: "turn-1", Status: appserver.TurnStatusCompleted},
		{ID: "turn-2", Status: appserver.TurnStatusInProgress},
	})
	store.SetInputState(&ThreadInputState{Draft: "hello"})

	if store.ActiveTurn() != "turn-2" {
		t.Fatalf("active turn = %q, want turn-2", store.ActiveTurn())
	}
	snapshot := store.Snapshot()
	if snapshot.Session == nil || snapshot.Session.ThreadID != "thread-1" || snapshot.Session.ServiceTier == nil || *snapshot.Session.ServiceTier != "priority" {
		t.Fatalf("snapshot session = %#v", snapshot.Session)
	}
	if len(snapshot.Turns) != 2 || snapshot.Turns[1].ID != "turn-2" {
		t.Fatalf("snapshot turns = %#v", snapshot.Turns)
	}
	if snapshot.InputState == nil || snapshot.InputState.Draft != "hello" {
		t.Fatalf("snapshot input = %#v", snapshot.InputState)
	}

	*snapshot.Session.ServiceTier = "mutated"
	snapshot.InputState.Draft = "mutated"
	if *store.Session.ServiceTier != "priority" || store.InputState.Draft != "hello" {
		t.Fatalf("snapshot mutation leaked session/input: %#v %#v", store.Session, store.InputState)
	}
}

func TestThreadEventStoreSetTurnsAndRollbackMatchRust(t *testing.T) {
	store := NewThreadEventStore(4)
	store.SetTurns([]appserver.Turn{
		{ID: "turn-1", Status: appserver.TurnStatusInProgress},
		{ID: "turn-2", Status: appserver.TurnStatusCompleted},
	})
	if store.ActiveTurn() != "turn-1" {
		t.Fatalf("active turn after SetTurns = %q, want turn-1", store.ActiveTurn())
	}
	store.EnqueueRequest(ServerRequest{ID: "req-1", Kind: ServerRequestUserInput, TurnID: "turn-1", ItemID: "call-1"})
	if len(store.Buffer) == 0 || !store.HasPendingThreadUserInput() {
		t.Fatalf("expected buffered pending input")
	}

	store.ApplyThreadRollback([]appserver.Turn{{ID: "turn-rolled", Status: appserver.TurnStatusCompleted}})
	if len(store.Buffer) != 0 || store.HasPendingThreadUserInput() || store.ActiveTurn() != "" {
		t.Fatalf("rollback did not clear buffer/pending/active: buffer=%#v active=%q", store.Buffer, store.ActiveTurn())
	}
	if len(store.Turns) != 1 || store.Turns[0].ID != "turn-rolled" {
		t.Fatalf("rollback turns = %#v", store.Turns)
	}
}
