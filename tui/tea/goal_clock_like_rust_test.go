package tea

import (
	"testing"
	"time"

	"codex_go/appserver"
	codextui "codex_go/tui"
)

func TestActiveGoalClockTicksLikeRust(t *testing.T) {
	now := time.Unix(2_000, 0)
	model := NewModel(codextui.NewState(nil), Options{Width: 80, Height: 24})
	model.State.ThreadID = "thread-goal"
	model.State.Status = "running"
	model.taskStartedAt = now
	model.now = func() time.Time { return now }
	model.currentGoal = &appserver.Goal{
		ThreadID:        "thread-goal",
		Objective:       "keep moving",
		Status:          appserver.GoalActive,
		TimeUsedSeconds: 5,
	}
	model.goalObservedAt = now
	if got := model.goalTaskProgress(); got != "Goal active (5s)" {
		t.Fatalf("initial goal progress = %q", got)
	}
	if cmd := model.ensureGoalStatusTick(); cmd == nil {
		t.Fatal("active goal did not start a clock tick")
	}
	if cmd := model.ensureGoalStatusTick(); cmd != nil {
		t.Fatal("active goal started a duplicate clock tick")
	}

	now = now.Add(time.Second)
	updated, next := model.Update(goalTickMsg{})
	model = updated.(*Model)
	if got := model.goalTaskProgress(); got != "Goal active (6s)" {
		t.Fatalf("progress after tick = %q, want 6s", got)
	}
	if next == nil {
		t.Fatal("tick did not schedule the next active-goal refresh")
	}

	model.currentGoal.Status = appserver.GoalComplete
	updated, next = model.Update(goalTickMsg{})
	model = updated.(*Model)
	if next != nil || model.goalStatusTickActive {
		t.Fatalf("complete goal kept ticking: next=%v active=%v", next, model.goalStatusTickActive)
	}
}
