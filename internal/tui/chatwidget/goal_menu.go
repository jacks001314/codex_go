package chatwidget

import "codex_go/internal/appserver"

const GoalMenuViewID = "goal-menu"

const (
	GoalMenuActionEdit        UsageMenuAction = "goal_edit"
	GoalMenuActionPause       UsageMenuAction = "goal_pause"
	GoalMenuActionResume      UsageMenuAction = "goal_resume"
	GoalMenuActionLeavePaused UsageMenuAction = "goal_leave_paused"
	GoalMenuActionClear       UsageMenuAction = "goal_clear"
)

type GoalEditPromptView struct {
	Title       string
	Placeholder string
	InitialText string
	Status      appserver.GoalStatus
	TokenBudget *int64
}

func NewGoalMenuView(goal appserver.Goal) SelectionView {
	items := []SelectionItem{{
		ID:              "edit",
		Name:            "Edit goal",
		Description:     "Update the objective.",
		Action:          GoalMenuActionEdit,
		DismissOnSelect: true,
	}}
	switch goal.Status {
	case appserver.GoalActive:
		items = append(items, SelectionItem{
			ID:              "pause",
			Name:            "Pause goal",
			Description:     "Stop goal tracking until you resume it.",
			Action:          GoalMenuActionPause,
			DismissOnSelect: true,
		})
	case appserver.GoalPaused, appserver.GoalBlocked, appserver.GoalUsageLimited:
		items = append(items, SelectionItem{
			ID:              "resume",
			Name:            "Resume goal",
			Description:     "Mark it active and continue when idle.",
			Action:          GoalMenuActionResume,
			DismissOnSelect: true,
		})
	}
	items = append(items, SelectionItem{
		ID:              "clear",
		Name:            "Clear goal",
		Description:     "Remove this goal from the thread.",
		Action:          GoalMenuActionClear,
		DismissOnSelect: true,
	})
	return SelectionView{
		ViewID:      GoalMenuViewID,
		Title:       "Goal",
		Subtitle:    GoalStatusLabel(goal.Status),
		FooterHint:  standardPopupHintLine,
		AllowCancel: true,
		Items:       items,
	}
}

func NewGoalEditPromptView(goal appserver.Goal) GoalEditPromptView {
	return GoalEditPromptView{
		Title:       "Edit goal",
		Placeholder: "Type a goal objective and press Enter",
		InitialText: goal.Objective,
		Status:      EditedGoalStatus(goal.Status),
		TokenBudget: cloneGoalTokenBudget(goal.TokenBudget),
	}
}

func cloneGoalTokenBudget(value *int64) *int64 {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}
