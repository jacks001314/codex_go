package app

import (
	"testing"

	"codex_go/appserver"
)

// A long-running turn's start may fall out of the bounded replay buffer before
// the widget rebuilds. The snapshot must still carry that turn so steering
// remains attached to it (Rust #58998).
func TestThreadEventStoreSnapshotRetainsEvictedActiveTurn(t *testing.T) {
	store := NewThreadEventStore(1)
	store.EnqueueNotification(ServerEvent{
		Name:   ServerNotificationTurnStarted,
		TurnID: "turn-1",
		Turn:   &appserver.Turn{ID: "turn-1", Status: appserver.TurnStatusInProgress},
	})
	store.EnqueueNotification(ServerEvent{
		Name:   ServerNotificationItemCompleted,
		TurnID: "turn-1",
		Item:   &appserver.ThreadItem{ID: "item-1", Type: "message"},
	})
	if len(store.Buffer) != 1 || store.Buffer[0].Notification == nil || store.Buffer[0].Notification.Name != ServerNotificationItemCompleted {
		t.Fatalf("buffer = %#v, want only the item notification", store.Buffer)
	}
	snapshot := store.Snapshot()
	if len(snapshot.Turns) != 1 || snapshot.Turns[0].ID != "turn-1" {
		t.Fatalf("snapshot turns = %#v, want the evicted active turn", snapshot.Turns)
	}
	store.EnqueueNotification(ServerEvent{
		Name:   ServerNotificationTurnCompleted,
		TurnID: "turn-1",
		Turn:   &appserver.Turn{ID: "turn-1", Status: appserver.TurnStatusCompleted},
	})
	snapshot = store.Snapshot()
	if len(snapshot.Turns) != 0 {
		t.Fatalf("snapshot turns after completion = %#v, want none", snapshot.Turns)
	}
}
