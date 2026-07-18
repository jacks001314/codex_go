package chatwidget

import (
	"testing"

	"codex_go/appserver"
)

func TestGoalMenuViewActionsMatchRustStatuses(t *testing.T) {
	active := NewGoalMenuView(goalMenuTestGoal(appserver.GoalActive, nil))
	if got := goalMenuActionNames(active); !sameStrings(got, []string{"Edit goal", "Pause goal", "Clear goal"}) {
		t.Fatalf("active actions = %#v", got)
	}
	if active.Items[1].Action != GoalMenuActionPause || active.Items[1].Description == "" {
		t.Fatalf("active pause item = %#v", active.Items[1])
	}

	paused := NewGoalMenuView(goalMenuTestGoal(appserver.GoalPaused, nil))
	if got := goalMenuActionNames(paused); !sameStrings(got, []string{"Edit goal", "Resume goal", "Clear goal"}) {
		t.Fatalf("paused actions = %#v", got)
	}
	if paused.Items[1].Action != GoalMenuActionResume || paused.Items[1].Description == "" {
		t.Fatalf("paused resume item = %#v", paused.Items[1])
	}

	terminal := NewGoalMenuView(goalMenuTestGoal(appserver.GoalComplete, nil))
	if got := goalMenuActionNames(terminal); !sameStrings(got, []string{"Edit goal", "Clear goal"}) {
		t.Fatalf("terminal actions = %#v", got)
	}
}

func TestGoalEditPromptViewPreservesStatusAndBudgetMatchRust(t *testing.T) {
	budget := int64(80_000)
	paused := NewGoalEditPromptView(goalMenuTestGoal(appserver.GoalPaused, &budget))
	if paused.Title != "Edit goal" || paused.Placeholder != "Type a goal objective and press Enter" {
		t.Fatalf("prompt copy = %#v", paused)
	}
	if paused.InitialText != "ship goal parity" || paused.Status != appserver.GoalPaused {
		t.Fatalf("paused prompt = %#v", paused)
	}
	if paused.TokenBudget == nil || *paused.TokenBudget != budget {
		t.Fatalf("paused budget = %#v", paused.TokenBudget)
	}
	*paused.TokenBudget = 1
	if budget != 80_000 {
		t.Fatal("prompt should clone token budget")
	}

	complete := NewGoalEditPromptView(goalMenuTestGoal(appserver.GoalComplete, &budget))
	if complete.Status != appserver.GoalActive {
		t.Fatalf("complete prompt status = %s", complete.Status)
	}
}

func goalMenuActionNames(view SelectionView) []string {
	out := make([]string, 0, len(view.Items))
	for _, item := range view.Items {
		out = append(out, item.Name)
	}
	return out
}

func goalMenuTestGoal(status appserver.GoalStatus, budget *int64) appserver.Goal {
	return appserver.Goal{
		ThreadID:        "thread",
		Objective:       "ship goal parity",
		Status:          status,
		TokenBudget:     budget,
		TokensUsed:      12_500,
		TimeUsedSeconds: 90,
	}
}

func sameStrings(got []string, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
