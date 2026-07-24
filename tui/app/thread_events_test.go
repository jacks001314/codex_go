package app

import (
	"testing"

	"codex_go/appserver"
)

func TestThreadEventStoreActiveTurnTrackingMatchRust(t *testing.T) {
	store := NewThreadEventStore(8)
	if got := store.ActiveTurn(); got != "" {
		t.Fatalf("initial active turn = %q, want empty", got)
	}
	store.EnqueueNotification(ServerEvent{Name: ServerNotificationTurnStarted, TurnID: "turn-1"})
	if got := store.ActiveTurn(); got != "turn-1" {
		t.Fatalf("active turn after start = %q, want turn-1", got)
	}
	store.EnqueueNotification(ServerEvent{Name: ServerNotificationTurnCompleted, TurnID: "turn-other"})
	if got := store.ActiveTurn(); got != "turn-1" {
		t.Fatalf("active turn after other completion = %q, want turn-1", got)
	}
	store.EnqueueNotification(ServerEvent{Name: ServerNotificationTurnCompleted, TurnID: "turn-1"})
	if got := store.ActiveTurn(); got != "" {
		t.Fatalf("active turn after completion = %q, want empty", got)
	}
	store.EnqueueNotification(ServerEvent{Name: ServerNotificationTurnStarted, Target: EventTarget{TurnID: "turn-2"}})
	if got := store.ActiveTurn(); got != "turn-2" {
		t.Fatalf("active turn from target = %q, want turn-2", got)
	}
	store.EnqueueNotification(ServerEvent{Name: ServerNotificationThreadClosed})
	if got := store.ActiveTurn(); got != "" {
		t.Fatalf("active turn after thread closed = %q, want empty", got)
	}
}

func TestThreadEventStoreCoalescesAndClearsPendingInterrupt(t *testing.T) {
	store := NewThreadEventStore(8)
	if !store.BeginInterrupt("turn-1") {
		t.Fatal("first interrupt should be accepted")
	}
	if store.BeginInterrupt("turn-1") {
		t.Fatal("repeated interrupt should be coalesced")
	}
	store.EnqueueNotification(ServerEvent{Name: ServerNotificationTurnCompleted, TurnID: "turn-other"})
	if got := store.PendingInterrupt(); got != "turn-1" {
		t.Fatalf("unrelated completion cleared pending interrupt: %q", got)
	}
	store.EnqueueNotification(ServerEvent{Name: ServerNotificationTurnCompleted, TurnID: "turn-1"})
	if got := store.PendingInterrupt(); got != "" {
		t.Fatalf("completed interrupt remained pending: %q", got)
	}
	store.BeginInterrupt("turn-2")
	store.EnqueueNotification(ServerEvent{Name: ServerNotificationThreadClosed})
	if got := store.PendingInterrupt(); got != "" {
		t.Fatalf("closed thread retained pending interrupt: %q", got)
	}
}

func TestThreadEventSurvivesSessionRefreshMatchRust(t *testing.T) {
	cases := []struct {
		event ThreadBufferedEvent
		want  bool
	}{
		{ThreadBufferedEvent{Type: ThreadBufferedEventRequest, Request: &ServerRequest{ID: "req"}}, true},
		{ThreadBufferedEvent{Type: ThreadBufferedEventFeedbackSubmission}, true},
		{ThreadBufferedEvent{Type: ThreadBufferedEventNotification, Notification: &ServerEvent{Name: ServerNotificationHookStarted}}, true},
		{ThreadBufferedEvent{Type: ThreadBufferedEventNotification, Notification: &ServerEvent{Name: ServerNotificationHookCompleted}}, true},
		{ThreadBufferedEvent{Type: ThreadBufferedEventNotification, Notification: &ServerEvent{Name: ServerNotificationMcpServerStatusUpdated}}, true},
		{ThreadBufferedEvent{Type: ThreadBufferedEventNotification, Notification: &ServerEvent{Name: ServerNotificationWarning}}, false},
		{ThreadBufferedEvent{Type: ThreadBufferedEventHistoryEntryResponse}, false},
	}
	for _, tc := range cases {
		if got := ThreadEventSurvivesSessionRefresh(tc.event); got != tc.want {
			t.Fatalf("ThreadEventSurvivesSessionRefresh(%#v) = %v, want %v", tc.event, got, tc.want)
		}
	}
}

func TestThreadEventStoreRebaseBufferAfterSessionRefreshMatchRust(t *testing.T) {
	store := NewThreadEventStore(8)
	store.EnqueueNotification(ServerEvent{Name: ServerNotificationWarning})
	store.EnqueueRequest(ServerRequest{ID: "req", Kind: ServerRequestUserInput, TurnID: "turn-1", ItemID: "call-1"})
	store.EnqueueNotification(ServerEvent{Name: ServerNotificationHookStarted})
	store.Buffer = append(store.Buffer, ThreadBufferedEvent{Type: ThreadBufferedEventFeedbackSubmission})
	store.Buffer = append(store.Buffer, ThreadBufferedEvent{Type: ThreadBufferedEventHistoryEntryResponse})

	store.RebaseBufferAfterSessionRefresh()

	if len(store.Buffer) != 3 {
		t.Fatalf("rebased buffer len = %d, want 3: %#v", len(store.Buffer), store.Buffer)
	}
	if store.Buffer[0].Type != ThreadBufferedEventRequest || store.Buffer[1].Notification.Name != ServerNotificationHookStarted || store.Buffer[2].Type != ThreadBufferedEventFeedbackSubmission {
		t.Fatalf("rebased buffer = %#v", store.Buffer)
	}
	if !store.HasPendingThreadUserInput() {
		t.Fatal("request should remain pending after rebase")
	}
}

func TestThreadEventChannelAttachmentMatchRust(t *testing.T) {
	channel := NewThreadEventChannel(8)
	if channel.Store == nil || channel.Attachment() != ThreadEventAttachmentLive {
		t.Fatalf("new channel = %#v attachment=%q", channel, channel.Attachment())
	}
	channel.MarkReplayOnly()
	if channel.Attachment() != ThreadEventAttachmentReplayOnly {
		t.Fatalf("attachment = %q, want replay only", channel.Attachment())
	}

	session := ThreadSessionState{ThreadID: "thread-1", CWD: "/repo"}
	withSession := NewThreadEventChannelWithSession(8, session, []appserver.Turn{{ID: "turn-1", Status: appserver.TurnStatusInProgress}})
	if withSession.Store == nil || withSession.Store.Session == nil || withSession.Store.Session.ThreadID != "thread-1" || withSession.Store.ActiveTurn() != "turn-1" {
		t.Fatalf("channel with session = %#v", withSession.Store)
	}
}

func TestThreadEventStoreFileChangeChangesMatchRust(t *testing.T) {
	store := NewThreadEventStore(8)
	store.SetTurns([]appserver.Turn{{
		ID:     "turn-1",
		Status: appserver.TurnStatusCompleted,
		Items: []appserver.ThreadItem{{
			ID:   "patch-1",
			Type: "fileChange",
			Data: map[string]any{
				"changes": []any{map[string]any{
					"path": "old.go",
					"kind": map[string]any{"type": "delete"},
					"diff": "-old",
				}},
			},
		}},
	}})

	changes, ok := store.FileChangeChanges("turn-1", "patch-1")
	if !ok || len(changes) != 1 || changes[0].Path != "old.go" || changes[0].Kind.Type != "delete" {
		t.Fatalf("turn file changes = %#v ok=%v", changes, ok)
	}

	store.EnqueueNotification(ServerEvent{
		Name:   ServerNotificationItemCompleted,
		TurnID: "turn-1",
		Item: &appserver.ThreadItem{
			ID:   "patch-1",
			Type: "fileChange",
			Data: map[string]any{
				"changes": []appserver.FileUpdateChange{{
					Path: "new.go",
					Kind: appserver.PatchChangeKind{Type: "add"},
					Diff: "+new",
				}},
			},
		},
	})
	changes, ok = store.FileChangeChanges("turn-1", "patch-1")
	if !ok || len(changes) != 1 || changes[0].Path != "new.go" || changes[0].Kind.Type != "add" {
		t.Fatalf("buffer file changes = %#v ok=%v", changes, ok)
	}

	changes, ok = store.FileChangeChanges("", "patch-1")
	if !ok || len(changes) != 1 || changes[0].Path != "new.go" {
		t.Fatalf("empty turn id should match latest patch = %#v ok=%v", changes, ok)
	}

	if changes, ok = store.FileChangeChanges("turn-missing", "patch-1"); ok || len(changes) != 0 {
		t.Fatalf("missing turn changes = %#v ok=%v", changes, ok)
	}

	if changes, ok = store.FileChangeChanges(" turn-1 ", "patch-1"); ok || len(changes) != 0 {
		t.Fatalf("spaced turn id changes = %#v ok=%v, want none", changes, ok)
	}
	if changes, ok = store.FileChangeChanges("turn-1", " patch-1 "); ok || len(changes) != 0 {
		t.Fatalf("spaced item id changes = %#v ok=%v, want none", changes, ok)
	}

	emptyIDStore := NewThreadEventStore(8)
	emptyIDStore.SetTurns([]appserver.Turn{{
		ID: "turn-empty",
		Items: []appserver.ThreadItem{{
			ID:   "",
			Type: "fileChange",
			Data: map[string]any{
				"changes": []appserver.FileUpdateChange{{Path: "empty.go"}},
			},
		}},
	}})
	changes, ok = emptyIDStore.FileChangeChanges("turn-empty", "")
	if !ok || len(changes) != 1 || changes[0].Path != "empty.go" {
		t.Fatalf("empty item id changes = %#v ok=%v", changes, ok)
	}
}

func TestFileUpdateChangesFromAnyParsesMovePath(t *testing.T) {
	changes := FileUpdateChangesFromAny([]map[string]any{{
		"path": "renamed.go",
		"kind": map[string]any{"type": "update", "movePath": "old.go"},
		"diff": "@@",
	}})
	if len(changes) != 1 || changes[0].Path != "renamed.go" || changes[0].Kind.Type != "update" || changes[0].Kind.MovePath == nil || *changes[0].Kind.MovePath != "old.go" || changes[0].Diff != "@@" {
		t.Fatalf("changes = %#v", changes)
	}

	changes = FileUpdateChangesFromAny([]map[string]any{{
		"path": " renamed.go ",
		"kind": map[string]any{"type": " update ", "movePath": ""},
		"diff": " @@ ",
	}})
	if len(changes) != 1 || changes[0].Path != " renamed.go " || changes[0].Kind.Type != " update " || changes[0].Kind.MovePath == nil || *changes[0].Kind.MovePath != "" || changes[0].Diff != " @@ " {
		t.Fatalf("changes preserving whitespace = %#v", changes)
	}
}
