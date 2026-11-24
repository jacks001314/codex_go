package appserver

import (
	"context"
	"testing"

	"codex_go/agent"
)

// TestOrdinaryMailboxWakeWaitsForTurnAdmissionLikeRust mirrors Rust #62537: an
// ordinary automatic mailbox wake takes a turn admission permit, so a workspace
// pause that holds admission closed leaves the mail queued instead of starting
// a turn, and releasing the pause retries the same wake.
func TestOrdinaryMailboxWakeWaitsForTurnAdmissionLikeRust(t *testing.T) {
	router, _ := newAgentMessageWakeTestRouter(t, true)

	pause, err := router.threads.PauseTurnAdmission(context.Background())
	if err != nil {
		t.Fatalf("PauseTurnAdmission() error = %v", err)
	}
	router.enqueueRuntimeAgentMessage("root", runtimeAgentCommunicationInputItem("/root/worker", "/root", "Queued while paused.", false, true))
	if active := router.threads.ActiveTurn("root"); active != nil {
		router.cancelActiveRuntimeTurn("root", active.TurnID)
		t.Fatalf("paused admission still started a wake turn: %#v", active)
	}
	preserved := router.drainRuntimeAgentMessages("root")
	if len(preserved) != 1 {
		t.Fatalf("queued mail during pause = %#v, want the single message preserved", preserved)
	}
	router.requeueRuntimeAgentMessages("root", preserved)

	pause.Release()

	active := router.threads.ActiveTurn("root")
	if active == nil || active.Params == nil {
		t.Fatal("reopening admission did not retry the queued wake")
	}
	defer router.cancelActiveRuntimeTurn("root", active.TurnID)
	if len(active.Params.AdditionalInputItems) != 1 {
		t.Fatalf("wake input items = %#v, want the preserved message", active.Params.AdditionalInputItems)
	}
	assertAgentMessageRoute(t, active.Params.AdditionalInputItems[0], "/root/worker", "/root")
	if queued := router.drainRuntimeAgentMessages("root"); len(queued) != 0 {
		t.Fatalf("retried wake left mail queued: %#v", queued)
	}
	// The wake reserved with a real admission permit, not the bypass marker.
	permit := router.threads.admittedTurns["root"]
	if permit == nil || permit == turnAdmissionBypassPermit {
		t.Fatalf("wake reservation permit = %#v, want an admitted turn", permit)
	}
}

// TestNativeChildResultWakesParentDuringDrainLikeRust mirrors the second half of
// Rust #62537: a native child's terminal result finishes already-admitted work,
// so it wakes an idle parent even while the server drains.
func TestNativeChildResultWakesParentDuringDrainLikeRust(t *testing.T) {
	router, _ := newAgentMessageWakeTestRouter(t, true)
	router.threads.BeginDraining()

	router.deliverRuntimeAgentCompletion("child", agent.AgentMessageStatus{
		Kind:    agent.AgentMessageStatusCompleted,
		Message: "Child finished before shutdown.",
	})

	active := router.threads.ActiveTurn("root")
	if active == nil || active.Params == nil {
		t.Fatal("draining refused the native child result wake")
	}
	defer router.cancelActiveRuntimeTurn("root", active.TurnID)
	if len(active.Params.AdditionalInputItems) != 1 {
		t.Fatalf("wake input items = %#v, want the child result", active.Params.AdditionalInputItems)
	}
	if !runtimeAgentMessageIsTerminalResult(active.Params.AdditionalInputItems[0]) {
		t.Fatalf("wake input = %#v, want a terminal child result", active.Params.AdditionalInputItems[0])
	}
	if permit := router.threads.admittedTurns["root"]; permit != turnAdmissionBypassPermit {
		t.Fatalf("child result reservation permit = %#v, want the bypass marker", permit)
	}
}

// TestOrdinaryMailboxWakeStaysQueuedDuringDrainLikeRust keeps plain mailbox
// mail gated: only explicit follow-ups and terminal results may start a turn
// once draining begins.
func TestOrdinaryMailboxWakeStaysQueuedDuringDrainLikeRust(t *testing.T) {
	router, _ := newAgentMessageWakeTestRouter(t, true)
	router.threads.BeginDraining()

	router.enqueueRuntimeAgentMessage("root", runtimeAgentCommunicationInputItem("/root/worker", "/root", "Queued during drain.", false, true))

	if active := router.threads.ActiveTurn("root"); active != nil {
		router.cancelActiveRuntimeTurn("root", active.TurnID)
		t.Fatalf("draining started an ordinary wake turn: %#v", active)
	}
	if queued := router.drainRuntimeAgentMessages("root"); len(queued) != 1 {
		t.Fatalf("queued mail during drain = %#v, want the single message preserved", queued)
	}
}

// TestRecordedMailWakeRetriesWhenAdmissionReopensLikeRust covers the recorded
// history wake: mail already persisted onto the root has no queued copy, so the
// release of a workspace pause re-applies the wake rules instead of redelivering
// anything.
func TestRecordedMailWakeRetriesWhenAdmissionReopensLikeRust(t *testing.T) {
	router, _ := newRecordedAgentMailWakeRouter(t, true)

	pause, err := router.threads.PauseTurnAdmission(context.Background())
	if err != nil {
		t.Fatalf("PauseTurnAdmission() error = %v", err)
	}
	if router.maybeWakeRootAfterRecordedAgentMail("root", recordMailItems(t, "/root/worker", "/root", "report ready")) {
		t.Fatal("paused admission accepted the recorded-mail wake")
	}
	if active := router.threads.ActiveTurn("root"); active != nil {
		router.cancelActiveRuntimeTurn("root", active.TurnID)
		t.Fatalf("paused admission started a recorded-mail wake: %#v", active)
	}

	pause.Release()

	active := router.threads.ActiveTurn("root")
	if active == nil || active.Params == nil {
		t.Fatal("reopening admission did not retry the recorded-mail wake")
	}
	defer router.cancelActiveRuntimeTurn("root", active.TurnID)
	if !active.Params.AutomaticWake {
		t.Fatalf("retried wake params = %#v, want AutomaticWake", active.Params)
	}
	if len(active.Params.AdditionalInputItems) != 0 {
		t.Fatalf("retried wake input items = %#v, want the recorded history copy only", active.Params.AdditionalInputItems)
	}
}
