package chatwidget

import (
	"testing"
	"time"
)

func TestTurnLifecycleStartFinishAndRestoreMatchRust(t *testing.T) {
	state := NewTurnLifecycleState(false)
	now := time.Unix(10, 0)

	state.StartAt("turn-1", now)
	if !state.AgentTurnRunning || !state.SleepInhibitorTurnRunning || !state.GoalStatusActiveTurnStartedAtOK {
		t.Fatalf("started state = %#v", state)
	}
	if !state.GoalStatusActiveTurnStartedAt.Equal(now) {
		t.Fatalf("started at = %v, want %v", state.GoalStatusActiveTurnStartedAt, now)
	}

	state.Finish()
	if state.AgentTurnRunning || state.SleepInhibitorTurnRunning || state.GoalStatusActiveTurnStartedAtOK {
		t.Fatalf("finished state = %#v", state)
	}

	later := time.Unix(20, 0)
	state.RestoreRunning(true, later)
	if !state.AgentTurnRunning || !state.SleepInhibitorTurnRunning || !state.GoalStatusActiveTurnStartedAt.Equal(later) {
		t.Fatalf("restored running state = %#v", state)
	}
	state.RestoreRunning(false, later)
	if state.AgentTurnRunning || state.SleepInhibitorTurnRunning || state.GoalStatusActiveTurnStartedAtOK {
		t.Fatalf("restored idle state = %#v", state)
	}
}

func TestTurnLifecycleResetAndBudgetLimitedMatchRust(t *testing.T) {
	state := NewTurnLifecycleState(true)
	state.StartAt("turn-1", time.Unix(10, 0))
	state.MarkBudgetLimited("turn-1")
	state.MarkBudgetLimited("turn-2")

	if !state.TakeBudgetLimited("turn-1") {
		t.Fatal("turn-1 budget flag was not consumed")
	}
	if state.TakeBudgetLimited("turn-1") {
		t.Fatal("turn-1 budget flag should only be consumed once")
	}

	state.ResetThread()
	if state.AgentTurnRunning || state.LastTurnID != "" || len(state.BudgetLimitedTurnIDs) != 0 {
		t.Fatalf("reset state = %#v", state)
	}
	if state.TakeBudgetLimited("turn-2") {
		t.Fatal("reset should clear budget-limited turns")
	}
}

func TestTurnLifecycleCompleteKeepsCurrentTurnGuard(t *testing.T) {
	state := NewTurnLifecycleState(false)
	state.StartAt("turn-1", time.Unix(10, 0))
	if state.Complete("turn-2") {
		t.Fatal("mismatched turn should not complete")
	}
	if !state.AgentTurnRunning {
		t.Fatalf("mismatched completion changed running state = %#v", state)
	}
	if !state.Complete("turn-1") {
		t.Fatal("current turn should complete")
	}
	if state.AgentTurnRunning {
		t.Fatalf("completed state = %#v", state)
	}
}
