package chatwidget

import "strings"

type ReasoningShortcutDirection string

const (
	ReasoningShortcutLower ReasoningShortcutDirection = "lower"
	ReasoningShortcutRaise ReasoningShortcutDirection = "raise"
)

type ReasoningShortcut struct {
	Key         string
	Effort      string
	Description string
}

type ReasoningEffortOption struct {
	Effort string
}

type ReasoningModelPreset struct {
	Model                     string
	DefaultReasoningEffort    string
	SupportedReasoningEfforts []ReasoningEffortOption
}

type ReasoningShortcutContext struct {
	SessionConfigured         bool
	ModalOrPopupActive        bool
	CollaborationModesEnabled bool
	PlanModeActive            bool
	CurrentModel              string
	ConfiguredReasoningEffort string
	Preset                    *ReasoningModelPreset
}

type ReasoningShortcutAction string

const (
	ReasoningShortcutIgnored           ReasoningShortcutAction = "ignored"
	ReasoningShortcutInfo              ReasoningShortcutAction = "info"
	ReasoningShortcutApplyNormal       ReasoningShortcutAction = "apply_normal"
	ReasoningShortcutApplyPlanOverride ReasoningShortcutAction = "apply_plan_override"
)

type ReasoningShortcutDecision struct {
	Handled   bool
	Action    ReasoningShortcutAction
	Model     string
	Effort    string
	Info      string
	Direction ReasoningShortcutDirection
}

func DefaultReasoningShortcuts() []ReasoningShortcut {
	return []ReasoningShortcut{
		{Key: "1", Effort: "minimal", Description: "Minimal reasoning"},
		{Key: "2", Effort: "low", Description: "Low reasoning"},
		{Key: "3", Effort: "medium", Description: "Medium reasoning"},
		{Key: "4", Effort: "high", Description: "High reasoning"},
	}
}

func ReasoningShortcutByKey(key string, shortcuts []ReasoningShortcut) (ReasoningShortcut, bool) {
	key = strings.TrimSpace(key)
	for _, shortcut := range shortcuts {
		if strings.TrimSpace(shortcut.Key) == key {
			return shortcut, true
		}
	}
	return ReasoningShortcut{}, false
}

func ReasoningChoices(preset ReasoningModelPreset) []string {
	choices := make([]string, 0, len(preset.SupportedReasoningEfforts))
	for _, option := range preset.SupportedReasoningEfforts {
		effort := strings.TrimSpace(option.Effort)
		if effort != "" {
			choices = append(choices, effort)
		}
	}
	if len(choices) == 0 && strings.TrimSpace(preset.DefaultReasoningEffort) != "" {
		choices = append(choices, strings.TrimSpace(preset.DefaultReasoningEffort))
	}
	return choices
}

func NextReasoningEffort(choices []string, currentEffort string, direction ReasoningShortcutDirection) (string, bool) {
	currentEffort = strings.TrimSpace(currentEffort)
	if currentEffort == "" {
		return "", false
	}
	currentIndex := -1
	for index, choice := range choices {
		if choice == currentEffort {
			currentIndex = index
			break
		}
	}
	if currentIndex < 0 {
		return "", false
	}
	switch direction {
	case ReasoningShortcutLower:
		if currentIndex == 0 {
			return "", false
		}
		return choices[currentIndex-1], true
	case ReasoningShortcutRaise:
		if currentIndex+1 >= len(choices) {
			return "", false
		}
		return choices[currentIndex+1], true
	default:
		return "", false
	}
}

func ResolveReasoningShortcutCurrentEffort(preset ReasoningModelPreset, configuredEffort string) string {
	choices := ReasoningChoices(preset)
	configuredEffort = strings.TrimSpace(configuredEffort)
	if containsReasoningChoice(choices, configuredEffort) {
		return configuredEffort
	}
	if containsReasoningChoice(choices, preset.DefaultReasoningEffort) {
		return strings.TrimSpace(preset.DefaultReasoningEffort)
	}
	if len(choices) > 0 {
		return choices[0]
	}
	return strings.TrimSpace(preset.DefaultReasoningEffort)
}

func DecideReasoningShortcut(direction ReasoningShortcutDirection, context ReasoningShortcutContext) ReasoningShortcutDecision {
	if direction != ReasoningShortcutLower && direction != ReasoningShortcutRaise {
		return ReasoningShortcutDecision{Action: ReasoningShortcutIgnored}
	}
	if context.ModalOrPopupActive {
		return ReasoningShortcutDecision{Action: ReasoningShortcutIgnored}
	}
	if !context.SessionConfigured {
		return ReasoningShortcutDecision{
			Handled:   true,
			Action:    ReasoningShortcutInfo,
			Info:      "Reasoning shortcuts are disabled until startup completes.",
			Direction: direction,
		}
	}
	if context.Preset == nil {
		return ReasoningShortcutDecision{
			Handled:   true,
			Action:    ReasoningShortcutInfo,
			Info:      "Reasoning shortcuts are unavailable for " + strings.TrimSpace(context.CurrentModel) + ".",
			Model:     strings.TrimSpace(context.CurrentModel),
			Direction: direction,
		}
	}

	choices := ReasoningChoices(*context.Preset)
	current := ResolveReasoningShortcutCurrentEffort(*context.Preset, context.ConfiguredReasoningEffort)
	next, ok := NextReasoningEffort(choices, current, direction)
	if !ok {
		return ReasoningShortcutDecision{
			Handled:   true,
			Action:    ReasoningShortcutInfo,
			Info:      ReasoningShortcutBoundMessage(direction, current),
			Model:     context.Preset.Model,
			Effort:    current,
			Direction: direction,
		}
	}

	action := ReasoningShortcutApplyNormal
	if context.CollaborationModesEnabled && context.PlanModeActive {
		action = ReasoningShortcutApplyPlanOverride
	}
	return ReasoningShortcutDecision{
		Handled:   true,
		Action:    action,
		Model:     context.Preset.Model,
		Effort:    next,
		Direction: direction,
	}
}

func ReasoningShortcutBoundMessage(direction ReasoningShortcutDirection, effort string) string {
	label := ReasoningEffortSentenceLabel(effort)
	switch direction {
	case ReasoningShortcutLower:
		return "Reasoning is already at the lowest level (" + label + ")."
	case ReasoningShortcutRaise:
		return "Reasoning is already at the highest level (" + label + ")."
	default:
		return ""
	}
}

func ReasoningEffortSentenceLabel(effort string) string {
	effort = strings.TrimSpace(effort)
	if effort == "" {
		return "default"
	}
	return strings.ReplaceAll(effort, "_", " ")
}

func containsReasoningChoice(choices []string, effort string) bool {
	effort = strings.TrimSpace(effort)
	for _, choice := range choices {
		if choice == effort {
			return true
		}
	}
	return false
}
