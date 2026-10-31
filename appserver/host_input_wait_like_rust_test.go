package appserver

import (
	"context"
	"testing"
	"time"

	"codex_go/turn"
)

// Mirrors Rust #58411: hosts observe already-queued agent mail without
// consuming it, report a timeout when nothing is pending, and keep queue-only
// mail for a later wait_agent call.
func TestRuntimeWaitForInputActivityPendingMailLikeRust(t *testing.T) {
	router := NewRuntimeRouter(RuntimeServices{})
	ctx := context.Background()
	router.enqueueRuntimeAgentMessage("thread", map[string]any{"type": "agent_message"})
	if got := router.WaitForInputActivity(ctx, "thread", "turn", time.Second); got != turn.InputWaitOutcomeMailboxActivity {
		t.Fatalf("WaitForInputActivity(queued mail) = %v, want MailboxActivity", got)
	}
	if got := router.drainRuntimeAgentMessages("thread"); len(got) != 1 {
		t.Fatalf("the wait consumed queued mail: %#v", got)
	}
	if got := router.WaitForInputActivity(ctx, "thread", "turn", 50*time.Millisecond); got != turn.InputWaitOutcomeTimedOut {
		t.Fatalf("WaitForInputActivity(no input) = %v, want TimedOut", got)
	}
}

// A live agent notification wakes the wait, leaves the notification for
// wait_agent, and steering input takes precedence when both are pending.
func TestRuntimeWaitForInputActivityLiveAndSteeringLikeRust(t *testing.T) {
	router := NewRuntimeRouter(RuntimeServices{})
	ctx := context.Background()
	done := make(chan turn.InputWaitOutcome, 1)
	go func() {
		done <- router.WaitForInputActivity(ctx, "thread", "turn-1", 5*time.Second)
	}()
	time.Sleep(20 * time.Millisecond)
	router.notifyRuntimeAgentActivity("thread", "worker is done")
	select {
	case got := <-done:
		if got != turn.InputWaitOutcomeMailboxActivity {
			t.Fatalf("WaitForInputActivity(live mail) = %v, want MailboxActivity", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("WaitForInputActivity did not wake for agent mail")
	}
	select {
	case message := <-router.runtimeAgentActivityMailbox("thread"):
		if message != "worker is done" {
			t.Fatalf("notification = %q, want the wait to leave it queued", message)
		}
	default:
		t.Fatal("the wait consumed the agent notification")
	}

	// A steering input queued for a replacement turn still wakes the wait that
	// named the original turn (Rust observes the session-wide activity source).
	replacement := make(chan turn.InputWaitOutcome, 1)
	go func() {
		replacement <- router.WaitForInputActivity(ctx, "other", "turn-1", 5*time.Second)
	}()
	time.Sleep(20 * time.Millisecond)
	if err := router.requireSteerMailbox().Enqueue(&turn.SteerEnqueueParams{ThreadID: "other", TurnID: "turn-2", InputItems: []any{map[string]any{"type": "message", "role": "user"}}}); err != nil {
		t.Fatalf("enqueue steer: %v", err)
	}
	select {
	case got := <-replacement:
		if got != turn.InputWaitOutcomeSteered {
			t.Fatalf("WaitForInputActivity(replacement steer) = %v, want Steered", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("WaitForInputActivity did not wake for the replacement turn")
	}

	// Steering takes precedence when the same turn also holds queued mail.
	router.enqueueRuntimeAgentMessage("third", map[string]any{"type": "agent_message"})
	if err := router.requireSteerMailbox().Enqueue(&turn.SteerEnqueueParams{ThreadID: "third", TurnID: "turn-1", InputItems: []any{map[string]any{"type": "message", "role": "user"}}}); err != nil {
		t.Fatalf("enqueue steer: %v", err)
	}
	if got := router.WaitForInputActivity(ctx, "third", "turn-1", time.Second); got != turn.InputWaitOutcomeSteered {
		t.Fatalf("WaitForInputActivity(mail and steer) = %v, want Steered", got)
	}
}

// Cancelling the wait returns the timeout outcome and leaves queued input and
// other observers untouched.
func TestRuntimeWaitForInputActivityCancellationLikeRust(t *testing.T) {
	router := NewRuntimeRouter(RuntimeServices{})
	cancelled, cancel := context.WithCancel(context.Background())
	done := make(chan turn.InputWaitOutcome, 1)
	go func() {
		done <- router.WaitForInputActivity(cancelled, "thread", "turn", 5*time.Second)
	}()
	time.Sleep(20 * time.Millisecond)
	cancel()
	select {
	case got := <-done:
		if got != turn.InputWaitOutcomeTimedOut {
			t.Fatalf("cancelled wait = %v, want TimedOut", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("cancelled wait did not return")
	}
	if err := router.requireSteerMailbox().Enqueue(&turn.SteerEnqueueParams{ThreadID: "thread", TurnID: "turn", InputItems: []any{map[string]any{"type": "message", "role": "user"}}}); err != nil {
		t.Fatalf("enqueue steer: %v", err)
	}
	if got := router.WaitForInputActivity(context.Background(), "thread", "turn", time.Second); got != turn.InputWaitOutcomeSteered {
		t.Fatalf("WaitForInputActivity(after cancellation) = %v, want Steered", got)
	}
}
