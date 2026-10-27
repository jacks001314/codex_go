package appserver

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"codex_go/config"
	"codex_go/rollout"
	"codex_go/turn"
)

// TestInterruptedWakeReservationPreservesMailAndStopsAutoWakeLikeRust mirrors
// Rust #56330: an interrupt during discovery rejects the reserved wake, keeps
// its mail queued for an explicit turn, and blocks later automatic turns until
// a new turn starts.
func TestInterruptedWakeReservationPreservesMailAndStopsAutoWakeLikeRust(t *testing.T) {
	router, _ := newAgentMessageWakeTestRouter(t, true)
	sink := NewNotificationBuffer()
	router.SetNotificationSink(sink)

	first := runtimeAgentCommunicationInputItem("/root/worker", "/root", "First deferred update.", false, true)
	turnID := registerTestWakeReservation(t, router, "root", []any{first})

	if _, err := router.handleTurnInterrupt(requestWithInternalParams(MethodTurnInterrupt, turn.TurnInterruptParams{ThreadID: "root", TurnID: turnID})); err != nil {
		t.Fatalf("handleTurnInterrupt() error = %v", err)
	}
	if active := router.threads.ActiveTurn("root"); active != nil {
		t.Fatalf("interrupted wake reservation remained active: %#v", active)
	}
	if !router.runtimeAgentWakesStopped("root") {
		t.Fatal("interrupted wake reservation did not mark automatic wakes stopped")
	}
	queued := router.drainRuntimeAgentMessages("root")
	if len(queued) != 1 {
		t.Fatalf("preserved mail = %#v, want the reserved message", queued)
	}
	assertAgentMessageRoute(t, queued[0], "/root/worker", "/root")
	if notifications := sink.List(); len(notifications) != 0 {
		t.Fatalf("rejected reservation emitted notifications: %#v", notifications)
	}

	router.requeueRuntimeAgentMessages("root", queued)
	second := runtimeAgentCommunicationInputItem("/root/worker", "/root", "Worker update after stop.", false, true)
	router.enqueueRuntimeAgentMessage("root", second)
	if active := router.threads.ActiveTurn("root"); active != nil {
		t.Fatalf("agent mail restarted automatic work after an interrupted wake: %#v", active)
	}
	queued = router.drainRuntimeAgentMessages("root")
	if len(queued) != 2 {
		t.Fatalf("queued mail after stop = %#v, want both messages preserved", queued)
	}
	router.requeueRuntimeAgentMessages("root", queued)

	// An explicit follow-up still consumes the preserved mail and clears the
	// stopped status, exactly like a TurnStarted event in Rust.
	response, err := router.handleTurnStart(requestWithInternalParams(MethodTurnStart, turn.TurnStartParams{ThreadID: "root", Prompt: "Continue the work."}))
	if err != nil {
		t.Fatalf("handleTurnStart() error = %v", err)
	}
	active := router.threads.ActiveTurn("root")
	if active == nil || active.TurnID != response.Turn.ID {
		t.Fatalf("explicit turn was not active: %#v", active)
	}
	defer router.cancelActiveRuntimeTurn("root", active.TurnID)
	if len(active.Params.AdditionalInputItems) != 2 {
		t.Fatalf("explicit turn input items = %#v, want both preserved messages", active.Params.AdditionalInputItems)
	}
	if router.runtimeAgentWakesStopped("root") {
		t.Fatal("starting an explicit turn did not clear the stopped wake status")
	}
}

// TestWakeReservationRejectedByPlanModePreservesMailLikeRust mirrors the
// post-discovery revalidation: a settings update can select Plan mode while
// the reserved wake is resolving its model catalog.
func TestWakeReservationRejectedByPlanModePreservesMailLikeRust(t *testing.T) {
	router, _ := newAgentMessageWakeTestRouter(t, true)
	sink := NewNotificationBuffer()
	router.SetNotificationSink(sink)

	item := runtimeAgentCommunicationInputItem("/root/worker", "/root", "Plan-mode deferred update.", false, true)
	params := &turn.TurnStartParams{ThreadID: "root", AutomaticWake: true, AdditionalInputItems: []any{item}}
	turnID := registerTestWakeReservationWithParams(t, router, "root", params)

	if _, err := router.requireThreadExtras().UpdateSettings(&SettingsUpdateParams{
		ThreadID:          "root",
		CollaborationMode: map[string]any{"mode": string(ModeKindPlan)},
	}); err != nil {
		t.Fatalf("UpdateSettings() error = %v", err)
	}
	if router.revalidateAutomaticWakeTurn(context.Background(), "root", turnID, params) {
		t.Fatal("Plan mode accepted an automatic wake")
	}
	if active := router.threads.ActiveTurn("root"); active != nil {
		t.Fatalf("rejected wake remained active: %#v", active)
	}
	if router.runtimeAgentWakesStopped("root") {
		t.Fatal("Plan-mode rejection must not mark the thread interrupted")
	}
	queued := router.drainRuntimeAgentMessages("root")
	if len(queued) != 1 {
		t.Fatalf("preserved mail = %#v, want one message", queued)
	}
	if notifications := sink.List(); len(notifications) != 0 {
		t.Fatalf("Plan-mode rejection emitted notifications: %#v", notifications)
	}
}

// TestWakeReservationKeepsAdmissionConfigurationLikeRust verifies that an
// accepted wake retains the settings captured at admission and consumes its
// reserved mail exactly once.
func TestWakeReservationKeepsAdmissionConfigurationLikeRust(t *testing.T) {
	router, _ := newAgentMessageWakeTestRouter(t, true)

	item := runtimeAgentCommunicationInputItem("/root/worker", "/root", "Accepted update.", false, true)
	effort := "high"
	params := &turn.TurnStartParams{
		ThreadID:               "root",
		Model:                  "gpt-wake-test",
		Effort:                 &effort,
		CoreCyberAccessProgram: "daybreak_red",
		AutomaticWake:          true,
		AdditionalInputItems:   []any{item},
	}
	turnID := registerTestWakeReservationWithParams(t, router, "root", params)

	if !router.revalidateAutomaticWakeTurn(context.Background(), "root", turnID, params) {
		t.Fatal("admitted wake was rejected without a control change")
	}
	active := router.threads.ActiveTurn("root")
	if active == nil || active.TurnID != turnID {
		t.Fatalf("accepted wake is not active: %#v", active)
	}
	if !active.TaskStarted {
		t.Fatal("accepted wake did not register its task")
	}
	if active.Params.Model != "gpt-wake-test" || active.Params.Effort == nil || *active.Params.Effort != "high" || active.Params.CoreCyberAccessProgram != "daybreak_red" {
		t.Fatalf("accepted wake configuration changed: %#v", active.Params)
	}
	if queued := router.drainRuntimeAgentMessages("root"); len(queued) != 0 {
		t.Fatalf("accepted wake left duplicate mail queued: %#v", queued)
	}
}

// TestFinishingTurnIgnoresInterruptLikeRust mirrors Rust #56330's completion
// protection: a turn that has detached its task but not persisted completion
// stays Running and records only TurnComplete, with the feature both enabled
// and disabled.
func TestFinishingTurnIgnoresInterruptLikeRust(t *testing.T) {
	for _, enabled := range []bool{true, false} {
		name := "disabled"
		if enabled {
			name = "enabled"
		}
		t.Run(name, func(t *testing.T) {
			router, _ := newAgentMessageWakeTestRouter(t, enabled)
			sink := NewNotificationBuffer()
			router.SetNotificationSink(sink)

			params := &turn.TurnStartParams{ThreadID: "root", Prompt: "Finish the work."}
			response, err := router.requireTurns().Start(params)
			if err != nil {
				t.Fatalf("Turns.Start() error = %v", err)
			}
			if err := router.threads.RegisterTurn("root", response.Turn.ID, func() {}, time.Now().UTC().UnixMilli(), params); err != nil {
				t.Fatalf("RegisterTurn() error = %v", err)
			}
			release := router.beginRuntimeTurnFinish("root", response.Turn.ID)

			if _, err := router.handleTurnInterrupt(requestWithInternalParams(MethodTurnInterrupt, turn.TurnInterruptParams{ThreadID: "root", TurnID: response.Turn.ID})); err != nil {
				t.Fatalf("handleTurnInterrupt() error = %v", err)
			}
			if active := router.threads.ActiveTurn("root"); active == nil {
				t.Fatal("interrupt aborted a finishing turn")
			}
			if notifications := sink.List(); len(notifications) != 0 {
				t.Fatalf("finishing turn emitted terminal notifications: %#v", notifications)
			}
			release()

			if !router.consumeCompletedRuntimeTurn("root", response.Turn.ID) {
				t.Fatal("finishing turn could not complete after the interrupt")
			}
			completedAt := time.Now().UTC()
			if err := router.appendRuntimeTurnComplete("root", response.Turn.ID, completedAt, 1); err != nil {
				t.Fatalf("appendRuntimeTurnComplete() error = %v", err)
			}
			router.completeTurnRecord("root", response.Turn.ID, TurnStatusCompleted)

			path, err := rollout.FindThreadPath(router.codexHomeForRollout(), "root", false)
			if err != nil || strings.TrimSpace(path) == "" {
				t.Fatalf("FindThreadPath(root) = %q, %v", path, err)
			}
			completes, aborts := countRuntimeTerminalRolloutEvents(t, path)
			if completes != 1 || aborts != 0 {
				t.Fatalf("terminal rollout events = %d complete, %d aborted; want one completion and no abort", completes, aborts)
			}
		})
	}
}

// TestExplicitTurnConsumesPreservedAgentMailLikeRust covers the feature-off and
// stopped-reservation cases: preserved mail waits for, and is delivered by, the
// next explicit turn.
func TestExplicitTurnConsumesPreservedAgentMailLikeRust(t *testing.T) {
	tests := []struct {
		name   string
		setup  func(t *testing.T, router *RuntimeRouter)
		before int
	}{
		{
			name: "feature disabled",
			setup: func(t *testing.T, router *RuntimeRouter) {
				writeAgentWakeFeature(t, router.config.CodexHome(), false)
			},
		},
		{
			name: "stopped wake reservation",
			setup: func(t *testing.T, router *RuntimeRouter) {
				router.markRuntimeAgentWakesStopped("root")
			},
			before: 1,
		},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			router, _ := newAgentMessageWakeTestRouter(t, true)
			testCase.setup(t, router)
			item := runtimeAgentCommunicationInputItem("/root/worker", "/root", "Preserved update.", false, true)
			router.enqueueRuntimeAgentMessage("root", item)
			if active := router.threads.ActiveTurn("root"); active != nil {
				t.Fatalf("preserved mail started automatic work: %#v", active)
			}

			response, err := router.handleTurnStart(requestWithInternalParams(MethodTurnStart, turn.TurnStartParams{ThreadID: "root", Prompt: "Read the worker update."}))
			if err != nil {
				t.Fatalf("handleTurnStart() error = %v", err)
			}
			active := router.threads.ActiveTurn("root")
			if active == nil || active.TurnID != response.Turn.ID {
				t.Fatalf("explicit turn was not active: %#v", active)
			}
			defer router.cancelActiveRuntimeTurn("root", active.TurnID)
			if len(active.Params.AdditionalInputItems) != 1 {
				t.Fatalf("explicit turn input items = %#v, want the preserved message", active.Params.AdditionalInputItems)
			}
			assertAgentMessageRoute(t, active.Params.AdditionalInputItems[0], "/root/worker", "/root")
			if queued := router.drainRuntimeAgentMessages("root"); len(queued) != 0 {
				t.Fatalf("explicit turn left mail queued: %#v", queued)
			}
			if router.runtimeAgentWakesStopped("root") {
				t.Fatal("explicit turn did not clear the stopped wake status")
			}
		})
	}
}

// TestExplicitTurnAdmissionFailurePreservesAgentMailLikeRust verifies that mail
// drained for an explicit turn returns to the queue when admission loses to a
// concurrent turn, so it can be delivered by the next accepted turn.
func TestExplicitTurnAdmissionFailurePreservesAgentMailLikeRust(t *testing.T) {
	router, _ := newAgentMessageWakeTestRouter(t, true)

	busy := &turn.TurnStartParams{ThreadID: "root", Prompt: "Hold the thread."}
	busyResponse, err := router.requireTurns().Start(busy)
	if err != nil {
		t.Fatalf("Turns.Start() error = %v", err)
	}
	if err := router.threads.RegisterTurn("root", busyResponse.Turn.ID, func() {}, time.Now().UTC().UnixMilli(), busy); err != nil {
		t.Fatalf("RegisterTurn() error = %v", err)
	}
	defer router.cancelActiveRuntimeTurn("root", busyResponse.Turn.ID)

	item := runtimeAgentCommunicationInputItem("/root/worker", "/root", "Deferred update.", false, true)
	router.requeueRuntimeAgentMessages("root", []any{item})

	if _, err := router.handleTurnStart(requestWithInternalParams(MethodTurnStart, turn.TurnStartParams{ThreadID: "root", Prompt: "Start anyway."})); err == nil {
		t.Fatal("handleTurnStart() succeeded while the thread already had an active turn")
	}
	queued := router.drainRuntimeAgentMessages("root")
	if len(queued) != 1 {
		t.Fatalf("mail lost on failed admission: %#v", queued)
	}
	assertAgentMessageRoute(t, queued[0], "/root/worker", "/root")
}

// TestAutomaticWakeBuildFailurePreservesMailLikeRust covers the post-admission
// build failure: a wake that cannot build its runtime must requeue its mail
// instead of emitting an aborted turn.
func TestAutomaticWakeBuildFailurePreservesMailLikeRust(t *testing.T) {
	router, _ := newAgentMessageWakeTestRouter(t, true)
	sink := NewNotificationBuffer()
	router.SetNotificationSink(sink)

	item := runtimeAgentCommunicationInputItem("/root/worker", "/root", "Update for a broken turn.", false, true)
	params := &turn.TurnStartParams{
		ThreadID:             "root",
		AutomaticWake:        true,
		AdditionalInputItems: []any{item},
	}
	response, err := router.requireTurns().Start(&turn.TurnStartParams{ThreadID: "root"})
	if err != nil {
		t.Fatalf("Turns.Start() error = %v", err)
	}
	// Force the post-admission runtime build to fail.
	if err := os.WriteFile(config.ConfigPath(router.config.CodexHome()), []byte("not = [valid"), 0o600); err != nil {
		t.Fatalf("write invalid config error = %v", err)
	}
	router.startTurnRuntimeAsync(params, response, "", nil)

	deadline := time.Now().Add(5 * time.Second)
	for {
		if active := router.threads.ActiveTurn("root"); active != nil {
			if time.Now().After(deadline) {
				t.Fatalf("automatic wake remained active after a build failure: %#v", active)
			}
			time.Sleep(10 * time.Millisecond)
			continue
		}
		queued := router.drainRuntimeAgentMessages("root")
		if len(queued) == 0 && time.Now().Before(deadline) {
			time.Sleep(10 * time.Millisecond)
			continue
		}
		if len(queued) != 1 {
			t.Fatalf("preserved mail = %#v, want one message", queued)
		}
		assertAgentMessageRoute(t, queued[0], "/root/worker", "/root")
		break
	}
	if notifications := sink.List(); len(notifications) != 0 {
		t.Fatalf("build failure emitted notifications: %#v", notifications)
	}
	if router.runtimeAgentWakesStopped("root") {
		t.Fatal("build failure must not mark the thread interrupted")
	}
}

func registerTestWakeReservation(t *testing.T, router *RuntimeRouter, threadID string, items []any) string {
	t.Helper()
	return registerTestWakeReservationWithParams(t, router, threadID, &turn.TurnStartParams{
		ThreadID:             threadID,
		AutomaticWake:        true,
		AdditionalInputItems: items,
	})
}

func registerTestWakeReservationWithParams(t *testing.T, router *RuntimeRouter, threadID string, params *turn.TurnStartParams) string {
	t.Helper()
	response, err := router.requireTurns().Start(&turn.TurnStartParams{ThreadID: threadID})
	if err != nil {
		t.Fatalf("Turns.Start() error = %v", err)
	}
	params.ThreadID = threadID
	params.AutomaticWake = true
	if err := router.threads.RegisterTurn(threadID, response.Turn.ID, func() {}, time.Now().UTC().UnixMilli(), params); err != nil {
		t.Fatalf("RegisterTurn() error = %v", err)
	}
	return response.Turn.ID
}

func countRuntimeTerminalRolloutEvents(t *testing.T, path string) (int, int) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%s) error = %v", path, err)
	}
	completes, aborts := 0, 0
	for _, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var event struct {
			Payload struct {
				Type string `json:"type"`
			} `json:"payload"`
		}
		if json.Unmarshal([]byte(line), &event) != nil {
			continue
		}
		switch event.Payload.Type {
		case "task_complete":
			completes++
		case "turn_aborted":
			aborts++
		}
	}
	return completes, aborts
}
