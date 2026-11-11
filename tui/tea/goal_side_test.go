package tea

import (
	"errors"
	"strings"
	"testing"

	bubbletea "charm.land/bubbletea/v2"

	"codex_go/appserver"
	codextui "codex_go/tui"
	keymsg "codex_go/tui/keymsg"
)

// A side conversation is an ephemeral fork with no goal state. Completing a
// side turn must not refresh or continue the parent thread's goal.
func TestModelGoalDoesNotLeakIntoSideConversation(t *testing.T) {
	state := codextui.NewState(nil)
	state.SetThreadID("thread-parent")
	reads := 0
	continuations := 0
	model := NewModel(state, Options{
		Width:  100,
		Height: 24,
		OnStartSide: func(params SideStartParams) (SideStartResponse, error) {
			return SideStartResponse{ParentThreadID: params.ParentThreadID, SideThreadID: "thread-side"}, nil
		},
		OnReadGoal: func(threadID string) (*appserver.Goal, error) {
			reads++
			if threadID == "thread-side" {
				return nil, errors.New("ephemeral thread does not support goals: thread-side")
			}
			return &appserver.Goal{ThreadID: threadID, Objective: "ship parity", Status: appserver.GoalActive}, nil
		},
		OnGoalContinuation: func(appserver.Goal, string) bubbletea.Cmd {
			continuations++
			return nil
		},
	})
	model.applyGoalUpdated(appserver.Goal{ThreadID: "thread-parent", Objective: "ship parity", Status: appserver.GoalActive}, false)
	if model.currentGoal == nil {
		t.Fatal("parent goal was not applied")
	}

	typeText(t, model, "/side")
	_, cmd := model.Update(key(keymsg.KeyEnter))
	runTeaCmd(t, model, cmd)
	if state.ThreadID != "thread-side" {
		t.Fatalf("thread after /side = %q, want thread-side", state.ThreadID)
	}
	if model.currentGoal != nil {
		t.Fatalf("side conversation inherited parent goal: %#v", model.currentGoal)
	}

	_, cmd = model.Update(TurnCompletedMsg{ThreadID: "thread-side", AssistantMessage: "side answer"})
	runTeaCmd(t, model, cmd)
	if reads != 0 {
		t.Fatalf("side turn refreshed a goal %d times, want 0", reads)
	}
	if continuations != 0 {
		t.Fatalf("side turn continued a goal %d times, want 0", continuations)
	}
	if view := model.viewString(); strings.Contains(view, "Goals need a saved session") {
		t.Fatalf("side turn surfaced an ephemeral goal error:\n%s", view)
	}

	model.Update(key(keymsg.KeyCtrlC))
	if state.ThreadID != "thread-parent" {
		t.Fatalf("thread after side return = %q, want thread-parent", state.ThreadID)
	}
	if model.currentGoal == nil || model.currentGoal.Objective != "ship parity" {
		t.Fatalf("parent goal after side return = %#v", model.currentGoal)
	}
}

func TestModelGoalRefreshErrorStaysInBackground(t *testing.T) {
	state := codextui.NewState(nil)
	state.SetThreadID("thread-goal")
	model := NewModel(state, Options{
		Width:  100,
		Height: 24,
		OnReadGoal: func(string) (*appserver.Goal, error) {
			return nil, errors.New("ephemeral thread does not support goals: thread-goal")
		},
		OnGoalContinuation: func(appserver.Goal, string) bubbletea.Cmd {
			return nil
		},
	})
	model.currentGoal = &appserver.Goal{ThreadID: "thread-goal", Objective: "1+1", Status: appserver.GoalActive}

	_, cmd := model.Update(TurnCompletedMsg{ThreadID: "thread-goal", AssistantMessage: "done"})
	if cmd == nil {
		t.Fatal("turn completion with an active goal did not return a refresh command")
	}
	runTeaCmd(t, model, cmd)
	if view := model.viewString(); strings.Contains(view, "Goals need a saved session") || strings.Contains(view, "Failed to read thread goal") {
		t.Fatalf("background goal refresh error surfaced:\n%s", view)
	}
}

func TestModelGoalNotificationForAnotherThreadDoesNotSwitchDisplay(t *testing.T) {
	state := codextui.NewState(nil)
	state.SetThreadID("thread-a")
	model := NewModel(state, Options{Width: 100, Height: 24})

	model.Update(GoalUpdatedMsg{Goal: appserver.Goal{
		ThreadID:  "thread-b",
		Objective: "other thread goal",
		Status:    appserver.GoalActive,
	}})
	if state.ThreadID != "thread-a" {
		t.Fatalf("goal notification switched displayed thread to %q", state.ThreadID)
	}
	if model.currentGoal != nil {
		t.Fatalf("displayed thread inherited another thread's goal: %#v", model.currentGoal)
	}

	model.switchThreadID("thread-b")
	if model.currentGoal == nil || model.currentGoal.Objective != "other thread goal" {
		t.Fatalf("stored goal after switching threads = %#v", model.currentGoal)
	}
}
