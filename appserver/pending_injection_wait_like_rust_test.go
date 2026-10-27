package appserver

import (
	"context"
	"testing"
	"time"

	"codex_go/agent"
	"codex_go/session"
	"codex_go/state"
	"codex_go/telemetry"
	"codex_go/turn"
)

// newPendingInjectionWaitRouter starts a blocked turn so wait_agent has an
// active turn with a steer mailbox to observe.
func newPendingInjectionWaitRouter(t *testing.T) (*RuntimeRouter, *runtimeAgentController, string, string) {
	t.Helper()
	metrics := state.NewTaskMetrics()
	runner := newBlockingAgent()
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(session.NewStore(t.TempDir())),
		Turns:        turn.NewTurnService(),
		Agent:        runner,
		ThreadStatus: NewThreadStatusManager(),
		TurnMetrics:  metrics,
	})
	t.Cleanup(func() { _ = router.Close() })
	cwd := t.TempDir()
	threadStart := router.Handle(requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{CWD: cwd}))
	if threadStart.Error != nil {
		t.Fatalf("thread/start: %+v", threadStart.Error)
	}
	threadID := threadStart.Result.(*ThreadStartResponse).Thread.ID
	turnStart := router.Handle(requestWithParams(t, IntID(2), MethodTurnStart, turn.TurnStartParams{ThreadID: threadID, Prompt: "wait for input"}))
	if turnStart.Error != nil {
		t.Fatalf("turn/start: %+v", turnStart.Error)
	}
	waitForBlockingAgentStart(t, runner)
	active := router.activeRuntimeTurnSnapshot(threadID)
	if active == nil || active.ID == "" {
		t.Fatal("turn/start did not create an active turn")
	}
	controller := newRuntimeAgentControllerWithVersion(router, threadID, cwd, 4, agent.VersionV2).(*runtimeAgentController)
	return router, controller, threadID, active.ID
}

// Mirrors Rust #55644: an injection queued before wait_agent starts still
// interrupts the wait and is reported as steering, not as mailbox activity.
func TestPendingInjectionInterruptsWaitAgentLikeRust(t *testing.T) {
	router, controller, threadID, turnID := newPendingInjectionWaitRouter(t)
	injection := map[string]any{
		"type": "message",
		"role": "developer",
		"content": []any{
			map[string]any{"type": "input_text", "text": "stop waiting and continue"},
		},
	}
	if err := router.requireSteerMailbox().Enqueue(&turn.SteerEnqueueParams{
		ThreadID: threadID, TurnID: turnID, InputItems: []any{injection},
	}); err != nil {
		t.Fatalf("enqueue injection: %v", err)
	}
	timeoutMS := int64(60_000)
	started := time.Now()
	result, err := controller.WaitForActivity(context.Background(), &agent.WaitForActivityArgs{TimeoutMS: &timeoutMS})
	if err != nil {
		t.Fatalf("WaitForActivity(): %v", err)
	}
	if result == nil || result.Message != "Wait interrupted by new input." || result.TimedOut {
		t.Fatalf("wait result = %#v, want the steered notice", result)
	}
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("wait returned after %s, want an immediate interruption", elapsed)
	}
	records := multiAgentWaitDurationRecords(router.services.TurnMetrics.(*state.TaskMetrics))
	if len(records) != 1 || records[0].Tags["outcome"] != telemetry.MultiAgentWaitOutcomeSteered {
		t.Fatalf("wait duration records = %#v, want one steered outcome", records)
	}
}

// A live injection interrupts a wait that is already running.
func TestLiveInjectionInterruptsWaitAgentLikeRust(t *testing.T) {
	router, controller, threadID, turnID := newPendingInjectionWaitRouter(t)
	timeoutMS := int64(60_000)
	done := make(chan *agent.WaitForActivityResult, 1)
	go func() {
		result, err := controller.WaitForActivity(context.Background(), &agent.WaitForActivityArgs{TimeoutMS: &timeoutMS})
		if err != nil {
			done <- nil
			return
		}
		done <- result
	}()
	time.Sleep(20 * time.Millisecond)
	if err := router.requireSteerMailbox().Enqueue(&turn.SteerEnqueueParams{
		ThreadID: threadID, TurnID: turnID,
		InputItems: []any{map[string]any{"type": "message", "role": "developer", "content": []any{}}},
	}); err != nil {
		t.Fatalf("enqueue injection: %v", err)
	}
	select {
	case result := <-done:
		if result == nil || result.Message != "Wait interrupted by new input." {
			t.Fatalf("wait result = %#v, want the steered notice", result)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("wait_agent did not wake for a live injection")
	}
}

// Queued inter-agent mail stays mailbox activity: it must not turn the wait into
// a steering interruption.
func TestQueuedMailDoesNotSteerWaitAgentLikeRust(t *testing.T) {
	router, controller, threadID, turnID := newPendingInjectionWaitRouter(t)
	if _, err := router.requireSteerMailbox().EnqueueIfAcceptingDelivery(&turn.SteerEnqueueParams{
		ThreadID: threadID, TurnID: turnID,
		InputItems: []any{map[string]any{"type": "agent_message", "author": "/root/worker"}},
	}); err != nil {
		t.Fatalf("enqueue mail: %v", err)
	}
	timeoutMS := int64(1)
	result, err := controller.WaitForActivity(context.Background(), &agent.WaitForActivityArgs{TimeoutMS: &timeoutMS})
	if err != nil {
		t.Fatalf("WaitForActivity(): %v", err)
	}
	if result == nil || !result.TimedOut {
		t.Fatalf("wait result = %#v, want a timeout for queued mail", result)
	}
}
