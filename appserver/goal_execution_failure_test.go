package appserver

import (
	"context"
	"testing"
	"time"

	"codex_go/state"
	"codex_go/tool"
	"codex_go/turn"
)

// goalExecFailureExecution builds a ToolExecutionResult for a code-mode `exec`
// tool call that either ran its handler and failed, completed successfully, or
// was blocked before the handler ran. Mirrors Rust #41454 record_tool_outcome's
// ToolCallOutcome classification.
func goalExecFailureExecution(name string, success bool, handlerExecuted bool) *turn.ToolExecutionResult {
	inv := &tool.Invocation{ToolName: tool.PlainName(name), CallID: "call-" + name, Payload: tool.Payload{Kind: tool.PayloadFunction, Arguments: `{}`}}
	output := &tool.Output{CallID: "call-" + name, ToolName: inv.ToolName, Success: success}
	if !success {
		output.Error = "exec failed"
	}
	return &turn.ToolExecutionResult{
		Invocation:      inv,
		Output:          output,
		Response:        turn.ToolResponseFromOutput(inv, output),
		FinishedAt:      time.Now().UTC(),
		HandlerExecuted: handlerExecuted,
	}
}

func TestGoalBlockedAfterRepeatedExecHostFailuresLikeRust(t *testing.T) {
	router, stateRuntime, threadID := newGoalToolTestRouter(t)
	ctx := context.Background()
	goal, err := stateRuntime.ReplaceThreadGoal(ctx, threadID, "exec failure goal", state.ThreadGoalActive, nil)
	if err != nil {
		t.Fatal(err)
	}

	// Three consecutive turns each run a handler-executed `exec` that fails.
	for i := 0; i < 3; i++ {
		turnID := "turn-" + string(rune('0'+i))
		router.markStateThreadGoalTurnActiveNow(threadID, turnID, goal.GoalID)
		router.recordGoalToolOutcome(threadID, turnID, goalExecFailureExecution(tool.CodeModeExecToolName, false, true))
		router.finishStateThreadGoalTurn(threadID, turnID, time.Now().UTC(), 0, nil)
	}

	updated, err := stateRuntime.GetThreadGoal(ctx, threadID)
	if err != nil || updated == nil {
		t.Fatalf("goal after exec failures = %#v, %v", updated, err)
	}
	if updated.Status != state.ThreadGoalBlocked {
		t.Fatalf("goal status = %q, want %q", updated.Status, state.ThreadGoalBlocked)
	}
}

func TestGoalExecutionFailuresDoNotBlockOnSuccessOrBlockedCall(t *testing.T) {
	router, stateRuntime, threadID := newGoalToolTestRouter(t)
	ctx := context.Background()
	goal, err := stateRuntime.ReplaceThreadGoal(ctx, threadID, "no block goal", state.ThreadGoalActive, nil)
	if err != nil {
		t.Fatal(err)
	}

	// A success resets the streak; a blocked (handler not executed) call does not
	// advance it. Even if we interleave failed exec turns, the counter never
	// reaches three consecutive failed exec turns.
	for i := 0; i < 3; i++ {
		turnID := "turn-" + string(rune('0'+i))
		router.markStateThreadGoalTurnActiveNow(threadID, turnID, goal.GoalID)
		router.recordGoalToolOutcome(threadID, turnID, goalExecFailureExecution(tool.CodeModeExecToolName, false, true))
		router.recordGoalToolOutcome(threadID, turnID, goalExecFailureExecution(tool.DefaultShellCommandToolName, true, true))
		router.finishStateThreadGoalTurn(threadID, turnID, time.Now().UTC(), 0, nil)
	}

	// A blocked (handler not executed) call never advances the streak.
	for i := 0; i < 3; i++ {
		turnID := "block-" + string(rune('0'+i))
		router.markStateThreadGoalTurnActiveNow(threadID, turnID, goal.GoalID)
		router.recordGoalToolOutcome(threadID, turnID, goalExecFailureExecution(tool.CodeModeExecToolName, false, false))
		router.finishStateThreadGoalTurn(threadID, turnID, time.Now().UTC(), 0, nil)
	}

	updated, err := stateRuntime.GetThreadGoal(ctx, threadID)
	if err != nil || updated == nil {
		t.Fatalf("goal = %#v, %v", updated, err)
	}
	if updated.Status != state.ThreadGoalActive {
		t.Fatalf("goal status = %q, want %q", updated.Status, state.ThreadGoalActive)
	}
}

func TestGoalExecutionFailuresDoNotTransferToReplacementGoal(t *testing.T) {
	router, stateRuntime, threadID := newGoalToolTestRouter(t)
	ctx := context.Background()
	first, err := stateRuntime.ReplaceThreadGoal(ctx, threadID, "first goal", state.ThreadGoalActive, nil)
	if err != nil {
		t.Fatal(err)
	}
	second, err := stateRuntime.ReplaceThreadGoal(ctx, threadID, "second goal", state.ThreadGoalActive, nil)
	if err != nil {
		t.Fatal(err)
	}

	// Two failed-exec turns for the first goal do not reach the block threshold.
	for i := 0; i < 2; i++ {
		turnID := "first-" + string(rune('0'+i))
		router.markStateThreadGoalTurnActiveNow(threadID, turnID, first.GoalID)
		router.recordGoalToolOutcome(threadID, turnID, goalExecFailureExecution(tool.CodeModeExecToolName, false, true))
		router.finishStateThreadGoalTurn(threadID, turnID, time.Now().UTC(), 0, nil)
	}

	// Replace the goal; two further failed-exec turns for the second goal should
	// also not reach the block threshold (streak anchored to the new goal).
	for i := 0; i < 2; i++ {
		turnID := "second-" + string(rune('0'+i))
		router.markStateThreadGoalTurnActiveNow(threadID, turnID, second.GoalID)
		router.recordGoalToolOutcome(threadID, turnID, goalExecFailureExecution(tool.CodeModeExecToolName, false, true))
		router.finishStateThreadGoalTurn(threadID, turnID, time.Now().UTC(), 0, nil)
	}

	updated, err := stateRuntime.GetThreadGoal(ctx, threadID)
	if err != nil || updated == nil {
		t.Fatalf("goal = %#v, %v", updated, err)
	}
	if updated.Status != state.ThreadGoalActive {
		t.Fatalf("goal status = %q, want %q", updated.Status, state.ThreadGoalActive)
	}
}
