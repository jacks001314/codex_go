package appserver

import (
	"errors"
	"testing"
	"time"

	"codex_go/session"
	"codex_go/turn"
)

// TestTerminalTurnNotificationsCarryStartTimeLikeRust pins Rust #61817:
// failed and interrupted turn completions report the turn's start timestamp,
// just like successful completions, so clients can order overlapping turns.
func TestTerminalTurnNotificationsCarryStartTimeLikeRust(t *testing.T) {
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter:   NewRouter(session.NewStore(t.TempDir())),
		Turns:          turn.NewTurnService(),
		ThreadStatus:   NewThreadStatusManager(),
		ServerRequests: NewServerRequestBroker(),
	})
	sink := NewNotificationBuffer()
	router.SetNotificationSink(sink)
	const threadID = "thread-started-at"
	const startedAtMS = int64(1_700_000_123_000)
	const wantStartedAt = startedAtMS / 1000

	router.finishTurnWithError(threadID, "turn-failed", startedAtMS, errors.New("boom"))
	router.finishTurnInterrupted(threadID, "turn-interrupted", startedAtMS)

	statuses := map[string]TurnStatus{}
	for _, notification := range sink.List() {
		if notification.Method != NotificationTurnCompleted {
			continue
		}
		payload, ok := notification.Params.(*TurnCompletedNotification)
		if !ok || payload == nil {
			continue
		}
		statuses[payload.Turn.ID] = payload.Turn.Status
		if payload.Turn.StartedAt == nil {
			t.Fatalf("%s completion has no startedAt: %#v", payload.Turn.ID, payload.Turn)
		}
		if got := *payload.Turn.StartedAt; got != wantStartedAt {
			t.Fatalf("%s startedAt = %d, want %d seconds", payload.Turn.ID, got, wantStartedAt)
		}
		if payload.Turn.CompletedAt == nil {
			t.Fatalf("%s completion has no completedAt: %#v", payload.Turn.ID, payload.Turn)
		}
	}
	if statuses["turn-failed"] != TurnStatusFailed {
		t.Fatalf("failed turn status = %q", statuses["turn-failed"])
	}
	if statuses["turn-interrupted"] != TurnStatusInterrupted {
		t.Fatalf("interrupted turn status = %q", statuses["turn-interrupted"])
	}
}

// TestOverlappingTurnsKeepTheirOwnCommandTrackingLikeRust pins Rust #61817:
// a turn can finish after the next one started, so command start times and
// lifecycle notifications must stay keyed by turn id. A late completion from
// the finished turn must neither clear nor duplicate the newer turn's item.
func TestOverlappingTurnsKeepTheirOwnCommandTrackingLikeRust(t *testing.T) {
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter:   NewRouter(session.NewStore(t.TempDir())),
		Turns:          turn.NewTurnService(),
		ThreadStatus:   NewThreadStatusManager(),
		ServerRequests: NewServerRequestBroker(),
	})
	sink := NewNotificationBuffer()
	router.SetNotificationSink(sink)
	const threadID = "thread-turn-isolation"
	command := "echo isolated"

	first := &CommandExecutionRequestApprovalParams{
		ThreadID:    threadID,
		TurnID:      "turn-1",
		ItemID:      "command",
		StartedAtMS: 1_000,
		Command:     &command,
	}
	if !router.openCommandApprovalItem(threadID, first.TurnID, first) {
		t.Fatal("first turn did not open its command item")
	}
	if !router.completeCommandApprovalItemDeclined(threadID, first.TurnID, first) {
		t.Fatal("first turn did not complete its command item")
	}

	// Finishing the first turn clears only that turn's tracking.
	router.clearItemStartedTimesForTurn(threadID, first.TurnID)

	second := &CommandExecutionRequestApprovalParams{
		ThreadID:    threadID,
		TurnID:      "turn-2",
		ItemID:      "command",
		StartedAtMS: 2_000,
		Command:     &command,
	}
	if !router.openCommandApprovalItem(threadID, second.TurnID, second) {
		t.Fatal("second turn did not open its command item")
	}

	// A late completion from the finished turn must be published under its own
	// turn and leave the new turn's tracking intact.
	before := len(sink.List())
	router.notify(NotificationItemCompleted, &ItemCompletedNotification{
		Item: ThreadItemPayload{
			"id":     "late-command",
			"type":   "commandExecution",
			"status": string(CommandExecutionFailed),
		},
		ThreadID:      threadID,
		TurnID:        first.TurnID,
		CompletedAtMS: time.Now().UTC().UnixMilli(),
	})
	if got := len(sink.List()); got != before+1 {
		t.Fatalf("late completion for the finished turn was dropped: %d -> %d notifications", before, got)
	}
	if startedAtMS := router.commandItemStartedAtMS(threadID, second.TurnID, "command"); startedAtMS != 2_000 {
		t.Fatalf("second turn start = %d, want 2000", startedAtMS)
	}
	if !router.completeCommandApprovalItemDeclined(threadID, second.TurnID, second) {
		t.Fatal("second turn did not complete its command item")
	}

	var completed []*ItemCompletedNotification
	for _, notification := range sink.List() {
		if notification.Method != NotificationItemCompleted {
			continue
		}
		payload, ok := notification.Params.(*ItemCompletedNotification)
		if !ok || payload == nil || payload.Item["id"] != "command" {
			continue
		}
		completed = append(completed, payload)
	}
	if len(completed) != 2 {
		t.Fatalf("command completions = %d, want one per turn", len(completed))
	}
	if completed[1].TurnID != second.TurnID || completed[1].Item["status"] != string(CommandExecutionDeclined) {
		t.Fatalf("second turn completion = %#v", completed[1])
	}
	if got, _ := completed[1].Item["startedAtMs"].(int64); got != 2_000 {
		t.Fatalf("second turn startedAtMs = %v, want 2000", completed[1].Item["startedAtMs"])
	}
}
