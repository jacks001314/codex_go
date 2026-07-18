package chatwidget

import "strings"

const (
	PlanImplementationViewID       = "plan-implementation"
	PlanImplementationTitle        = "Implement this plan?"
	PlanImplementationCodingText   = "Implement the plan."
	PlanImplementationClearPrefix  = "A previous agent produced the plan below to accomplish the user's task. Implement the plan in a fresh context. Treat the plan as the source of user intent, re-read files as needed, and carry the work through implementation and verification."
	PlanImplementationDefaultBlock = "Default mode unavailable"
	PlanImplementationNoPlanBlock  = "No approved plan available"
)

const (
	PlanImplementationActionImplement    UsageMenuAction = "plan_implementation_implement"
	PlanImplementationActionClearContext UsageMenuAction = "plan_implementation_clear_context"
	PlanImplementationActionStay         UsageMenuAction = "plan_implementation_stay"
)

type PlanImplementationConfig struct {
	DefaultModeAvailable   bool
	PlanMarkdown           string
	ClearContextUsageLabel string
}

func NewPlanImplementationView(config PlanImplementationConfig) SelectionView {
	implementDisabled := ""
	clearDisabled := ""
	if !config.DefaultModeAvailable {
		implementDisabled = PlanImplementationDefaultBlock
		clearDisabled = PlanImplementationDefaultBlock
	} else if strings.TrimSpace(config.PlanMarkdown) == "" {
		clearDisabled = PlanImplementationNoPlanBlock
	}
	clearDescription := "Fresh thread with this plan."
	if strings.TrimSpace(config.ClearContextUsageLabel) != "" {
		clearDescription = "Fresh thread. Context: " + strings.TrimSpace(config.ClearContextUsageLabel) + "."
	}
	return SelectionView{
		ViewID:      PlanImplementationViewID,
		Title:       PlanImplementationTitle,
		FooterHint:  standardPopupHintLine,
		AllowCancel: true,
		Items: []SelectionItem{
			{
				ID:              "implement",
				Name:            "Yes, implement this plan",
				Description:     "Switch to Default and start coding.",
				Disabled:        implementDisabled != "",
				DisabledReason:  implementDisabled,
				Action:          PlanImplementationActionImplement,
				DismissOnSelect: true,
			},
			{
				ID:              "clear-context",
				Name:            "Yes, clear context and implement",
				Description:     clearDescription,
				Disabled:        clearDisabled != "",
				DisabledReason:  clearDisabled,
				Action:          PlanImplementationActionClearContext,
				DismissOnSelect: true,
			},
			{
				ID:              "stay",
				Name:            "No, stay in Plan mode",
				Description:     "Continue planning with the model.",
				Action:          PlanImplementationActionStay,
				DismissOnSelect: true,
			},
		},
	}
}

func PlanImplementationClearContextPrompt(planMarkdown string) (string, bool) {
	planMarkdown = strings.TrimSpace(planMarkdown)
	if planMarkdown == "" {
		return "", false
	}
	return PlanImplementationClearPrefix + "\n\n" + planMarkdown, true
}
