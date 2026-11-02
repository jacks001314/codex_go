package app

import (
	"strings"
	"testing"

	"codex_go/appserver"
)

const realtimeDelegationPrompt = "<realtime_delegation><input>inspect the workspace</input></realtime_delegation>"

func delegationUserItem(id string) *appserver.ThreadItem {
	return &appserver.ThreadItem{ID: id, Type: "user_message", Text: realtimeDelegationPrompt}
}

// TestThreadEventStoreMarksDelegatedTurnLikeRust mirrors Rust
// push_notification_inner: the user message that carries the
// <realtime_delegation> envelope marks its turn, once, and the marker outlives
// the turn's completion.
func TestThreadEventStoreMarksDelegatedTurnLikeRust(t *testing.T) {
	store := NewThreadEventStore(8)
	store.EnqueueNotification(ServerEvent{
		Name:   ServerNotificationItemStarted,
		TurnID: "voice-turn",
		ItemID: "user-1",
		Item:   delegationUserItem("user-1"),
	})
	store.EnqueueNotification(ServerEvent{
		Name:   ServerNotificationItemCompleted,
		TurnID: "voice-turn",
		ItemID: "user-1",
		Item:   delegationUserItem("user-1"),
	})
	if got := store.DelegatedTurnIDs(); len(got) != 1 || got[0] != "voice-turn" {
		t.Fatalf("delegated turns = %#v, want [voice-turn]", got)
	}
	store.EnqueueNotification(ServerEvent{
		Name:   ServerNotificationTurnCompleted,
		TurnID: "voice-turn",
		Turn:   &appserver.Turn{ID: "voice-turn", Status: appserver.TurnStatusCompleted},
	})
	if !store.IsDelegatedTurn("voice-turn") {
		t.Fatal("turn completion dropped the delegated marker; replay would leak private items")
	}
	// A typed turn is never marked.
	store.EnqueueNotification(ServerEvent{
		Name:   ServerNotificationItemCompleted,
		TurnID: "typed-turn",
		ItemID: "user-2",
		Item:   &appserver.ThreadItem{ID: "user-2", Type: "user_message", Text: "typed by hand"},
	})
	if store.IsDelegatedTurn("typed-turn") {
		t.Fatal("a typed user message marked its turn delegated")
	}
}

// TestThreadEventSnapshotRestoresDelegatedTurnsLikeRust mirrors Rust
// ThreadEventSnapshot::delegated_turns + hide_private_items_after_voice_handoff:
// the rebuilt thread still knows the turn was delegated and replays only its
// public output.
func TestThreadEventSnapshotRestoresDelegatedTurnsLikeRust(t *testing.T) {
	store := NewThreadEventStore(8)
	store.SetTurns([]appserver.Turn{{
		ID: "voice-turn",
		Items: []appserver.ThreadItem{
			*delegationUserItem("user-1"),
			{ID: "reasoning-1", Type: "reasoning", Text: "private chain of thought"},
			{ID: "commentary-1", Type: "agent_message", Text: "private commentary", Data: map[string]any{"phase": "commentary"}},
			{ID: "agent-1", Type: "agent_message", Text: "the public answer", Data: map[string]any{"phase": "final_answer"}},
		},
	}, {
		ID: "typed-turn",
		Items: []appserver.ThreadItem{
			{ID: "user-2", Type: "user_message", Text: "typed question"},
			{ID: "reasoning-2", Type: "reasoning", Text: "typed reasoning"},
			{ID: "agent-2", Type: "agent_message", Text: "typed answer"},
		},
	}})
	store.EnqueueNotification(ServerEvent{
		Name:   ServerNotificationItemStarted,
		TurnID: "voice-turn",
		ItemID: "user-1",
		Item:   delegationUserItem("user-1"),
	})

	snapshot := store.Snapshot()
	if len(snapshot.DelegatedTurns) != 1 || snapshot.DelegatedTurns[0] != "voice-turn" {
		t.Fatalf("snapshot delegated turns = %#v", snapshot.DelegatedTurns)
	}
	voice := snapshot.Turns[0]
	assertTurnHasItems(t, voice, "user-1", "agent-1")
	typed := snapshot.Turns[1]
	assertTurnHasItems(t, typed, "user-2", "reasoning-2", "agent-2")

	// The store's own turns stay complete: pruning must work on the snapshot copy.
	live := store.Turns[0]
	if len(live.Items) != 4 {
		t.Fatalf("snapshot pruning mutated the live store: %d items", len(live.Items))
	}
}

func assertTurnHasItems(t *testing.T, turn appserver.Turn, want ...string) {
	t.Helper()
	got := make([]string, 0, len(turn.Items))
	for _, item := range turn.Items {
		got = append(got, item.ID)
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("turn %s items = %#v, want %#v", turn.ID, got, want)
	}
}

// TestThreadEventSnapshotDropsPrivateReplayEventsLikeRust mirrors the Rust
// buffered-event filter: after the voice marker, reasoning/commentary deltas and
// completions are dropped, while items that started as typed work survive.
func TestThreadEventSnapshotDropsPrivateReplayEventsLikeRust(t *testing.T) {
	store := NewThreadEventStore(16)
	store.EnqueueNotification(ServerEvent{
		Name:   ServerNotificationItemStarted,
		TurnID: "voice-turn",
		ItemID: "reasoning-typed",
		Item:   &appserver.ThreadItem{ID: "reasoning-typed", Type: "reasoning"},
	})
	store.EnqueueNotification(ServerEvent{
		Name:   ServerNotificationItemStarted,
		TurnID: "voice-turn",
		ItemID: "user-1",
		Item:   delegationUserItem("user-1"),
	})
	store.EnqueueNotification(ServerEvent{
		Name:   ServerNotificationReasoningSummaryTextDelta,
		TurnID: "voice-turn",
		ItemID: "reasoning-private",
	})
	store.EnqueueNotification(ServerEvent{
		Name:   ServerNotificationReasoningSummaryTextDelta,
		TurnID: "voice-turn",
		ItemID: "reasoning-typed",
	})
	store.EnqueueNotification(ServerEvent{
		Name:   ServerNotificationItemCompleted,
		TurnID: "voice-turn",
		ItemID: "reasoning-private",
		Item:   &appserver.ThreadItem{ID: "reasoning-private", Type: "reasoning", Text: "private"},
	})
	store.EnqueueNotification(ServerEvent{
		Name:   ServerNotificationItemCompleted,
		TurnID: "voice-turn",
		ItemID: "commentary-private",
		Item: &appserver.ThreadItem{ID: "commentary-private", Type: "agent_message", Text: "private commentary",
			Data: map[string]any{"phase": "commentary"}},
	})
	store.EnqueueNotification(ServerEvent{
		Name:   ServerNotificationItemCompleted,
		TurnID: "voice-turn",
		ItemID: "agent-1",
		Item:   &appserver.ThreadItem{ID: "agent-1", Type: "agent_message", Text: "the public answer"},
	})

	snapshot := store.Snapshot()
	var kept []string
	for _, event := range snapshot.Events {
		if event.Notification == nil {
			continue
		}
		kept = append(kept, event.Notification.Name+":"+realtimeDelegationItemID(*event.Notification))
	}
	joined := strings.Join(kept, " ")
	for _, dropped := range []string{"reasoning-private", "commentary-private"} {
		if strings.Contains(joined, dropped) {
			t.Fatalf("private event %s survived the snapshot: %s", dropped, joined)
		}
	}
	for _, required := range []string{
		"item_started:user-1",
		"reasoning_summary_text_delta:reasoning-typed",
		"item_completed:agent-1",
	} {
		if !strings.Contains(joined, required) {
			t.Fatalf("public event %s was dropped: %s", required, joined)
		}
	}
	if strings.Count(joined, "reasoning_summary_text_delta:reasoning-typed") != 1 {
		t.Fatalf("typed reasoning delta not kept exactly once: %s", joined)
	}
}

// TestThreadEventTrimKeepsDelegatedMarkerWithBufferedOutputLikeRust mirrors
// Rust trim_delegated_turns: an older marker without buffered output is evicted
// before one whose turn still has replayable output.
func TestThreadEventTrimKeepsDelegatedMarkerWithBufferedOutputLikeRust(t *testing.T) {
	store := NewThreadEventStore(1)
	store.EnqueueNotification(ServerEvent{
		Name:   ServerNotificationItemStarted,
		TurnID: "old-turn",
		ItemID: "user-old",
		Item:   delegationUserItem("user-old"),
	})
	store.EnqueueNotification(ServerEvent{
		Name:   ServerNotificationItemStarted,
		TurnID: "new-turn",
		ItemID: "user-new",
		Item:   delegationUserItem("user-new"),
	})
	store.EnqueueNotification(ServerEvent{
		Name:   ServerNotificationAgentMessageDelta,
		TurnID: "new-turn",
		ItemID: "agent-new",
	})
	if got := store.DelegatedTurnIDs(); len(got) != 1 || got[0] != "new-turn" {
		t.Fatalf("delegated turns = %#v, want [new-turn]", got)
	}
}
