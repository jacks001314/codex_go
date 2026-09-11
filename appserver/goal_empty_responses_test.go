package appserver

import (
	"context"
	"fmt"
	"testing"
	"time"

	"codex_go/model"
	"codex_go/state"
	"codex_go/tool"
	"codex_go/turn"
)

func emptyGoalFinalResult() *turn.AgentLoopResult {
	return &turn.AgentLoopResult{Response: &model.AgentResponse{Items: []model.AgentItem{{Type: "message"}}}}
}

func goalFinalResult(text string) *turn.AgentLoopResult {
	return &turn.AgentLoopResult{Response: &model.AgentResponse{Items: []model.AgentItem{{Type: "message", Text: text}}}}
}

func startAutomaticGoalTurn(router *RuntimeRouter, threadID, turnID, goalID string) {
	router.markStateThreadGoalTurnActiveNow(threadID, turnID, goalID)
	router.markGoalContinuation(threadID, turnID)
}

// TestGoalEmptyAutomaticContinuationsBlockAfterThree mirrors Rust #44320: three
// consecutive empty automatic continuations block the goal.
func TestGoalEmptyAutomaticContinuationsBlockAfterThree(t *testing.T) {
	router, stateRuntime, threadID := newGoalToolTestRouter(t)
	ctx := context.Background()
	goal, err := stateRuntime.InsertThreadGoal(ctx, threadID, "objective", state.ThreadGoalActive, nil)
	if err != nil || goal == nil {
		t.Fatalf("InsertThreadGoal() = %#v, %v", goal, err)
	}
	for index := 0; index < goalEmptyResponseThreshold; index++ {
		turnID := fmt.Sprintf("empty-%d", index)
		startAutomaticGoalTurn(router, threadID, turnID, goal.GoalID)
		router.recordGoalResultItems(threadID, turnID, &turn.TurnStartParams{ThreadID: threadID}, emptyGoalFinalResult())
		router.finishStateThreadGoalTurn(threadID, turnID, time.Now().UTC(), 0, nil)
	}
	updated, err := stateRuntime.GetThreadGoal(ctx, threadID)
	if err != nil || updated == nil {
		t.Fatalf("goal after empty continuations = %#v, %v", updated, err)
	}
	if updated.Status != state.ThreadGoalBlocked {
		t.Fatalf("goal status = %q, want %q", updated.Status, state.ThreadGoalBlocked)
	}
}

// TestGoalEmptyStreakResetsOnActivityAndNonAutomaticTurns verifies the streak
// resets on final-answer text, commentary, or tool activity, and that
// non-automatic turns never advance it.
func TestGoalEmptyStreakResetsOnActivityAndNonAutomaticTurns(t *testing.T) {
	router, stateRuntime, threadID := newGoalToolTestRouter(t)
	ctx := context.Background()
	goal, err := stateRuntime.InsertThreadGoal(ctx, threadID, "objective", state.ThreadGoalActive, nil)
	if err != nil || goal == nil {
		t.Fatalf("InsertThreadGoal() = %#v, %v", goal, err)
	}
	runEmpty := func(prefix string, automatic bool) {
		t.Helper()
		turnID := prefix + fmt.Sprintf("-%d", time.Now().UnixNano())
		router.markStateThreadGoalTurnActiveNow(threadID, turnID, goal.GoalID)
		if automatic {
			router.markGoalContinuation(threadID, turnID)
		}
		router.recordGoalResultItems(threadID, turnID, &turn.TurnStartParams{ThreadID: threadID}, emptyGoalFinalResult())
		router.finishStateThreadGoalTurn(threadID, turnID, time.Now().UTC(), 0, nil)
	}
	runEmpty("user", false)
	runEmpty("user", false)
	runEmpty("user", false)
	if updated, _ := stateRuntime.GetThreadGoal(ctx, threadID); updated == nil || updated.Status != state.ThreadGoalActive {
		t.Fatalf("non-automatic turns blocked the goal: %#v", updated)
	}

	runEmpty("auto", true)
	runEmpty("auto", true)
	// A recovered final answer resets the streak before the third empty turn.
	recoveryTurn := "recovery"
	startAutomaticGoalTurn(router, threadID, recoveryTurn, goal.GoalID)
	router.recordGoalResultItems(threadID, recoveryTurn, &turn.TurnStartParams{ThreadID: threadID}, goalFinalResult("progress"))
	router.finishStateThreadGoalTurn(threadID, recoveryTurn, time.Now().UTC(), 0, nil)
	// Tool activity also resets the streak.
	toolTurn := "tool-activity"
	startAutomaticGoalTurn(router, threadID, toolTurn, goal.GoalID)
	router.recordGoalToolOutcome(threadID, toolTurn, goalExecFailureExecution(tool.DefaultShellCommandToolName, true, true))
	router.recordGoalResultItems(threadID, toolTurn, &turn.TurnStartParams{ThreadID: threadID}, emptyGoalFinalResult())
	router.finishStateThreadGoalTurn(threadID, toolTurn, time.Now().UTC(), 0, nil)
	runEmpty("auto", true)
	runEmpty("auto", true)
	if updated, _ := stateRuntime.GetThreadGoal(ctx, threadID); updated == nil || updated.Status != state.ThreadGoalActive {
		t.Fatalf("activity did not reset the empty streak: %#v", updated)
	}
}

// TestGoalEmptyStreakResetsOnGoalChange verifies a replacement goal starts a
// fresh empty-continuation streak.
func TestGoalEmptyStreakResetsOnGoalChange(t *testing.T) {
	router, stateRuntime, threadID := newGoalToolTestRouter(t)
	ctx := context.Background()
	goal, err := stateRuntime.InsertThreadGoal(ctx, threadID, "first", state.ThreadGoalActive, nil)
	if err != nil || goal == nil {
		t.Fatalf("InsertThreadGoal() = %#v, %v", goal, err)
	}
	for index := 0; index < goalEmptyResponseThreshold-1; index++ {
		turnID := fmt.Sprintf("first-%d", index)
		startAutomaticGoalTurn(router, threadID, turnID, goal.GoalID)
		router.recordGoalResultItems(threadID, turnID, &turn.TurnStartParams{ThreadID: threadID}, emptyGoalFinalResult())
		router.finishStateThreadGoalTurn(threadID, turnID, time.Now().UTC(), 0, nil)
	}
	replacement, err := stateRuntime.UpdateThreadGoal(ctx, threadID, state.GoalUpdate{Status: ptrThreadGoalStatus(state.ThreadGoalComplete)})
	if err != nil || replacement == nil {
		t.Fatalf("complete first goal = %#v, %v", replacement, err)
	}
	second, err := stateRuntime.InsertThreadGoal(ctx, threadID, "second", state.ThreadGoalActive, nil)
	if err != nil || second == nil {
		t.Fatalf("InsertThreadGoal(second) = %#v, %v", second, err)
	}
	for index := 0; index < goalEmptyResponseThreshold-1; index++ {
		turnID := fmt.Sprintf("second-%d", index)
		startAutomaticGoalTurn(router, threadID, turnID, second.GoalID)
		router.recordGoalResultItems(threadID, turnID, &turn.TurnStartParams{ThreadID: threadID}, emptyGoalFinalResult())
		router.finishStateThreadGoalTurn(threadID, turnID, time.Now().UTC(), 0, nil)
	}
	if updated, _ := stateRuntime.GetThreadGoal(ctx, threadID); updated == nil || updated.Status != state.ThreadGoalActive {
		t.Fatalf("goal change did not reset the empty streak: %#v", updated)
	}
}

func ptrThreadGoalStatus(status state.ThreadGoalStatus) *state.ThreadGoalStatus {
	value := status
	return &value
}
