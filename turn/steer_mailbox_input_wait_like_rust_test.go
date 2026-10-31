package turn

import (
	"context"
	"testing"
	"time"
)

// Mirrors Rust #58411 host waits: already-buffered steering input is observed
// without consuming it, and agent mail is reported as mailbox activity.
func TestSteerMailboxWaitForInputActivityPendingLikeRust(t *testing.T) {
	mailbox := NewSteerMailbox()
	if err := mailbox.Enqueue(&SteerEnqueueParams{ThreadID: "t", TurnID: "turn", InputItems: []any{queuedUserMessage("queued")}}); err != nil {
		t.Fatalf("enqueue steer: %v", err)
	}
	if got := mailbox.WaitForInputActivity(context.Background(), "t", "turn", time.Second); got != InputWaitOutcomeSteered {
		t.Fatalf("WaitForInputActivity(pending steer) = %v, want Steered", got)
	}
	if !mailbox.HasPendingUserInput("t", "turn") {
		t.Fatalf("the wait consumed the queued steer")
	}

	mailbox.Clear(&SteerDrainParams{ThreadID: "t", TurnID: "turn"})
	if _, err := mailbox.EnqueueIfAcceptingDelivery(&SteerEnqueueParams{ThreadID: "t", TurnID: "turn", InputItems: []any{mailboxMailInputItem("/root/worker", "done")}}); err != nil {
		t.Fatalf("enqueue mail: %v", err)
	}
	if got := mailbox.WaitForInputActivity(context.Background(), "t", "turn", time.Second); got != InputWaitOutcomeMailboxActivity {
		t.Fatalf("WaitForInputActivity(pending mail) = %v, want MailboxActivity", got)
	}
	if !mailbox.HasPendingMailboxItems("t", "turn") {
		t.Fatalf("the wait consumed the queued mail")
	}
}

// Input arriving while a host waits wakes it, and the wait stays awake across
// a turn replacement: the named turn only supplies already-buffered input.
func TestSteerMailboxWaitForInputActivityIncomingLikeRust(t *testing.T) {
	mailbox := NewSteerMailbox()
	done := make(chan InputWaitOutcome, 1)
	go func() {
		done <- mailbox.WaitForInputActivity(context.Background(), "t", "turn-1", 5*time.Second)
	}()
	time.Sleep(20 * time.Millisecond)
	// A replacement turn of the same thread still wakes the wait (Rust observes
	// the session-wide activity source).
	if err := mailbox.Enqueue(&SteerEnqueueParams{ThreadID: "t", TurnID: "turn-2", InputItems: []any{queuedUserMessage("replacement")}}); err != nil {
		t.Fatalf("enqueue steer: %v", err)
	}
	select {
	case got := <-done:
		if got != InputWaitOutcomeSteered {
			t.Fatalf("WaitForInputActivity = %v, want Steered", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("WaitForInputActivity did not wake for the replacement turn")
	}
}

// Activity queued for another thread does not wake a wait, and a wait with no
// input reports the timeout outcome.
func TestSteerMailboxWaitForInputActivityScopedAndTimedOutLikeRust(t *testing.T) {
	mailbox := NewSteerMailbox()
	done := make(chan InputWaitOutcome, 1)
	go func() {
		done <- mailbox.WaitForInputActivity(context.Background(), "t1", "turn", 150*time.Millisecond)
	}()
	time.Sleep(20 * time.Millisecond)
	if err := mailbox.Enqueue(&SteerEnqueueParams{ThreadID: "t2", TurnID: "turn", InputItems: []any{queuedUserMessage("other thread")}}); err != nil {
		t.Fatalf("enqueue steer: %v", err)
	}
	select {
	case got := <-done:
		if got != InputWaitOutcomeTimedOut {
			t.Fatalf("WaitForInputActivity(other thread input) = %v, want TimedOut", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("WaitForInputActivity did not return")
	}
}

// Each call observes independently and leaving the wait (context cancellation)
// neither consumes input nor affects another observer.
func TestSteerMailboxWaitForInputActivityIndependentObserversLikeRust(t *testing.T) {
	mailbox := NewSteerMailbox()
	cancelled, cancel := context.WithCancel(context.Background())
	cancelledDone := make(chan InputWaitOutcome, 1)
	go func() {
		cancelledDone <- mailbox.WaitForInputActivity(cancelled, "t", "turn", 5*time.Second)
	}()
	first := make(chan InputWaitOutcome, 1)
	second := make(chan InputWaitOutcome, 1)
	go func() { first <- mailbox.WaitForInputActivity(context.Background(), "t", "turn", 5*time.Second) }()
	go func() { second <- mailbox.WaitForInputActivity(context.Background(), "t", "turn", 5*time.Second) }()
	time.Sleep(20 * time.Millisecond)
	cancel()
	select {
	case got := <-cancelledDone:
		if got != InputWaitOutcomeTimedOut {
			t.Fatalf("cancelled wait = %v, want TimedOut", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("cancelled wait did not return")
	}
	if err := mailbox.Enqueue(&SteerEnqueueParams{ThreadID: "t", TurnID: "turn", InputItems: []any{queuedUserMessage("input")}}); err != nil {
		t.Fatalf("enqueue steer: %v", err)
	}
	for i, ch := range []chan InputWaitOutcome{first, second} {
		select {
		case got := <-ch:
			if got != InputWaitOutcomeSteered {
				t.Fatalf("observer %d = %v, want Steered", i, got)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("observer %d did not wake", i)
		}
	}
	if drained := mailbox.Drain(&SteerDrainParams{ThreadID: "t", TurnID: "turn"}); len(drained) != 1 {
		t.Fatalf("drained input = %#v, want the single queued item", drained)
	}
}
