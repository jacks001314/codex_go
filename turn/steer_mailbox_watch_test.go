package turn

import (
	"testing"
	"time"

	"codex_go/tool"
)

func queuedUserMessage(text string) any {
	return map[string]any{
		"type": "message",
		"role": "user",
		"content": []any{
			map[string]any{"type": "input_text", "text": text},
		},
	}
}

// Mirrors Rust InputQueue::watch_user_input (#48135): the watcher is subscribed
// before the first check, so a message queued before or after the sampling
// request started both cancel the signal.
func TestSteerMailboxWatchUserInputCancelsOnQueuedUserMessage(t *testing.T) {
	mailbox := NewSteerMailbox()
	if err := mailbox.Enqueue(&SteerEnqueueParams{ThreadID: "t1", TurnID: "turn-1", InputItems: []any{queuedUserMessage("queued before")}}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	signal := tool.NewYieldSignal()
	stop := mailbox.WatchUserInput("t1", "turn-1", signal)
	if !signal.Cancelled() {
		t.Fatal("a message queued before the watch must cancel the signal")
	}
	stop()

	// An arrival after the watcher started is not missed.
	mailbox = NewSteerMailbox()
	signal = tool.NewYieldSignal()
	stop = mailbox.WatchUserInput("t1", "turn-1", signal)
	if signal.Cancelled() {
		t.Fatal("an empty queue must not cancel the signal")
	}
	if err := mailbox.Enqueue(&SteerEnqueueParams{ThreadID: "t1", TurnID: "turn-1", InputItems: []any{queuedUserMessage("queued after")}}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	waitForUserInputCancel(t, signal, true)
	stop()
}

// Agent mail and message-board notifications share the mailbox but are not
// instant-interrupt triggers (Rust TurnInputQueue::has_user_input).
func TestSteerMailboxWatchUserInputIgnoresNonUserInput(t *testing.T) {
	mailbox := NewSteerMailbox()
	signal := tool.NewYieldSignal()
	stop := mailbox.WatchUserInput("t1", "turn-1", signal)
	defer stop()
	if err := mailbox.Enqueue(&SteerEnqueueParams{ThreadID: "t1", TurnID: "turn-1", InputItems: []any{
		map[string]any{"type": "agent_message", "author": "agent-2"},
		map[string]any{"type": "agent_message_board_notification", "post_id": "p-1"},
	}}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	waitForUserInputCancel(t, signal, false)

	// Another turn's message is not this request's trigger either.
	if err := mailbox.Enqueue(&SteerEnqueueParams{ThreadID: "t1", TurnID: "turn-2", InputItems: []any{queuedUserMessage("other turn")}}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	waitForUserInputCancel(t, signal, false)
}

func TestSteerMailboxWatchUserInputReleasesWatcher(t *testing.T) {
	mailbox := NewSteerMailbox()
	signal := tool.NewYieldSignal()
	stop := mailbox.WatchUserInput("t1", "turn-1", signal)
	stop()
	if err := mailbox.Enqueue(&SteerEnqueueParams{ThreadID: "t1", TurnID: "turn-1", InputItems: []any{queuedUserMessage("after release")}}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	waitForUserInputCancel(t, signal, false)
}

// waitForUserInputCancel asserts the watcher's eventual state: a triggering
// watcher must cancel within the deadline, and a non-triggering one is observed
// across a short settle window so a late cancellation is still reported.
func waitForUserInputCancel(t *testing.T, signal *tool.YieldSignal, want bool) {
	t.Helper()
	if want {
		deadline := time.Now().Add(2 * time.Second)
		for !signal.Cancelled() {
			if time.Now().After(deadline) {
				t.Fatal("the watcher did not cancel the signal")
			}
			time.Sleep(2 * time.Millisecond)
		}
		return
	}
	for attempt := 0; attempt < 20; attempt++ {
		if signal.Cancelled() {
			t.Fatal("the watcher cancelled a signal it must not cancel")
		}
		time.Sleep(2 * time.Millisecond)
	}
}
