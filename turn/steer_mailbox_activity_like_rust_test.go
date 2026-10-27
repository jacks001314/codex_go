package turn

import (
	"testing"

	"codex_go/tool"
)

// Mirrors Rust #55644's input_queue coverage: a response item queued through a
// notifying injection reports steering activity even when it is not a user
// message, and consuming it clears the pending bit so later mailbox input does
// not steer a waiter.
func TestSteerMailboxPendingInjectionActivityLikeRust(t *testing.T) {
	mailbox := NewSteerMailbox()
	injected := map[string]any{
		"type": "message",
		"role": "developer",
		"content": []any{
			map[string]any{"type": "input_text", "text": "injected context"},
		},
	}
	if err := mailbox.Enqueue(&SteerEnqueueParams{ThreadID: "t", TurnID: "turn", InputItems: []any{injected}}); err != nil {
		t.Fatalf("enqueue injection: %v", err)
	}
	if got := mailbox.PendingActivity("t", "turn"); got != SteerMailboxActivitySteer {
		t.Fatalf("PendingActivity(injected response item) = %v, want Steer", got)
	}

	drained := mailbox.Drain(&SteerDrainParams{ThreadID: "t", TurnID: "turn"})
	if len(drained) != 1 {
		t.Fatalf("drained injection = %#v, want one item", drained)
	}
	if got := mailbox.PendingActivity("t", "turn"); got != SteerMailboxActivityNone {
		t.Fatalf("PendingActivity(after drain) = %v, want None", got)
	}
	if _, err := mailbox.EnqueueIfAcceptingDelivery(&SteerEnqueueParams{ThreadID: "t", TurnID: "turn", InputItems: []any{mailboxMailInputItem("/root/worker", "passive")}}); err != nil {
		t.Fatalf("enqueue mail: %v", err)
	}
	if got := mailbox.PendingActivity("t", "turn"); got != SteerMailboxActivityMailbox {
		t.Fatalf("PendingActivity(mail after a drained injection) = %v, want Mailbox", got)
	}

	// Clearing a queued injection also drops the pending bit.
	if err := mailbox.Enqueue(&SteerEnqueueParams{ThreadID: "t", TurnID: "turn", InputItems: []any{injected}}); err != nil {
		t.Fatalf("enqueue injection: %v", err)
	}
	mailbox.Clear(&SteerDrainParams{ThreadID: "t", TurnID: "turn"})
	if got := mailbox.PendingActivity("t", "turn"); got != SteerMailboxActivityNone {
		t.Fatalf("PendingActivity(after clear) = %v, want None", got)
	}
}

// A queued function-call output steers the turn (Rust pending_activity's
// FunctionCallOutput arm), while agent mail is only mailbox activity.
func TestSteerMailboxPendingActivityKindsLikeRust(t *testing.T) {
	mailbox := NewSteerMailbox()
	if _, err := mailbox.EnqueueIfAcceptingDelivery(&SteerEnqueueParams{ThreadID: "t", TurnID: "turn", InputItems: []any{mailboxMailInputItem("/root/worker", "mail")}}); err != nil {
		t.Fatalf("enqueue mail: %v", err)
	}
	if got := mailbox.PendingActivity("t", "turn"); got != SteerMailboxActivityMailbox {
		t.Fatalf("PendingActivity(mail) = %v, want Mailbox", got)
	}
	mailbox.Clear(&SteerDrainParams{ThreadID: "t", TurnID: "turn"})

	output := map[string]any{"type": "function_call_output", "call_id": "call-1", "output": "result"}
	if _, err := mailbox.EnqueueIfAcceptingDelivery(&SteerEnqueueParams{ThreadID: "t", TurnID: "turn", InputItems: []any{output}}); err != nil {
		t.Fatalf("enqueue output: %v", err)
	}
	if got := mailbox.PendingActivity("t", "turn"); got != SteerMailboxActivitySteer {
		t.Fatalf("PendingActivity(function_call_output) = %v, want Steer", got)
	}
}

// WatchActivity wakes a sleeping tool for either kind of activity, while
// WatchSteer only wakes a wait for steering input.
func TestSteerMailboxWatchActivityLikeRust(t *testing.T) {
	mailbox := NewSteerMailbox()
	activity := tool.NewYieldSignal()
	stopActivity := mailbox.WatchActivity("t", "turn", activity)
	defer stopActivity()
	steer := tool.NewYieldSignal()
	stopSteer := mailbox.WatchSteer("t", "turn", steer)
	defer stopSteer()

	if _, err := mailbox.EnqueueIfAcceptingDelivery(&SteerEnqueueParams{ThreadID: "t", TurnID: "turn", InputItems: []any{mailboxMailInputItem("/root/worker", "mail")}}); err != nil {
		t.Fatalf("enqueue mail: %v", err)
	}
	waitForUserInputCancel(t, activity, true)
	waitForUserInputCancel(t, steer, false)

	if err := mailbox.Enqueue(&SteerEnqueueParams{ThreadID: "t", TurnID: "turn", InputItems: []any{
		map[string]any{"type": "message", "role": "developer", "content": []any{}},
	}}); err != nil {
		t.Fatalf("enqueue injection: %v", err)
	}
	waitForUserInputCancel(t, steer, true)
}
