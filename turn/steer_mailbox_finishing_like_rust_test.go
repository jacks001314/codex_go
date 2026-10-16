package turn

import (
	"sync/atomic"
	"testing"
	"time"
)

// Mirrors Rust #53452 "Close input admission while a turn finishes recording":
// the turn's task finishing marks it as finishing before pending input is
// drained, so injection is rejected while the final batch is recorded, a late
// notification waits for the final batch, and a turn that is only reserved (not
// yet started) still accepts input.
func TestSteerMailboxFinishingClosesInputAdmissionLikeRust(t *testing.T) {
	mailbox := NewSteerMailbox()
	const threadID = "thread-finishing"
	const turnID = "turn-1"

	params := func(turn string) *SteerEnqueueParams {
		return &SteerEnqueueParams{
			ThreadID:   threadID,
			TurnID:     turn,
			InputItems: []any{map[string]any{"type": "text", "text": "child mail"}},
		}
	}

	// While the turn runs, the mailbox delivery path accepts input.
	if accepted, err := mailbox.EnqueueIfAcceptingDelivery(params(turnID)); err != nil || !accepted {
		t.Fatalf("EnqueueIfAcceptingDelivery(running turn) = %v, %v; want true, nil", accepted, err)
	}
	if mailbox.IsTurnFinishing(threadID, turnID) {
		t.Fatal("a running turn is not finishing")
	}

	// The task finished: admission closes while the final batch is recorded.
	release := mailbox.BeginTurnFinish(threadID, turnID)
	if !mailbox.IsTurnFinishing(threadID, turnID) {
		t.Fatal("BeginTurnFinish must mark the turn as finishing")
	}
	if accepted, err := mailbox.EnqueueIfAcceptingDelivery(params(turnID)); err != nil || accepted {
		t.Fatalf("EnqueueIfAcceptingDelivery(finishing turn) = %v, %v; want false, nil", accepted, err)
	}
	// The loop's drain gate agrees, so a finishing turn cannot reopen an answer
	// that is already being recorded.
	if mailbox.AcceptsMailboxDeliveryForCurrentTurn(threadID, turnID) {
		t.Fatal("a finishing turn must not accept mailbox delivery")
	}
	// A turn that is only reserved (a different turn id, or a later turn of the
	// same thread) still accepts input.
	if accepted, err := mailbox.EnqueueIfAcceptingDelivery(params("turn-2")); err != nil || !accepted {
		t.Fatalf("EnqueueIfAcceptingDelivery(reserved turn) = %v, %v; want true, nil", accepted, err)
	}

	// A late notification waits for the final batch instead of overtaking it.
	var waited atomic.Bool
	done := make(chan struct{})
	go func() {
		mailbox.WaitForTurnFinish(threadID, turnID)
		waited.Store(true)
		close(done)
	}()
	select {
	case <-done:
		t.Fatal("WaitForTurnFinish returned before the final batch was recorded")
	case <-time.After(50 * time.Millisecond):
	}

	// Recording the final batch releases the turn.
	release()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("WaitForTurnFinish did not wake after the release")
	}
	if !waited.Load() {
		t.Fatal("the waiter did not observe the release")
	}
	if mailbox.IsTurnFinishing(threadID, turnID) {
		t.Fatal("the release must clear the finishing state")
	}
	// Releasing twice is a no-op, so an explicit release beside a deferred one is
	// safe.
	release()

	// Input is admitted again once the final batch is recorded.
	if accepted, err := mailbox.EnqueueIfAcceptingDelivery(params(turnID)); err != nil || !accepted {
		t.Fatalf("EnqueueIfAcceptingDelivery(released turn) = %v, %v; want true, nil", accepted, err)
	}
	// A turn the mailbox never saw has no finishing entry, so waiting is free.
	mailbox.WaitForTurnFinish(threadID, "turn-unknown")
}
