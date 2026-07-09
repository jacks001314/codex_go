package chatwidget

import (
	"fmt"
	"time"

	"codex_go/internal/appserver"
)

type GoalStatusIndicatorKind string

const (
	GoalStatusIndicatorActive        GoalStatusIndicatorKind = "active"
	GoalStatusIndicatorPaused        GoalStatusIndicatorKind = "paused"
	GoalStatusIndicatorBlocked       GoalStatusIndicatorKind = "blocked"
	GoalStatusIndicatorUsageLimited  GoalStatusIndicatorKind = "usage_limited"
	GoalStatusIndicatorBudgetLimited GoalStatusIndicatorKind = "budget_limited"
	GoalStatusIndicatorComplete      GoalStatusIndicatorKind = "complete"
)

type GoalStatusIndicator struct {
	Kind  GoalStatusIndicatorKind
	Usage *string
}

type GoalStatusState struct {
	Goal       appserver.Goal
	ObservedAt time.Time
}

func NewGoalStatusState(goal appserver.Goal, observedAt time.Time) GoalStatusState {
	return GoalStatusState{Goal: goal, ObservedAt: observedAt}
}

func (s GoalStatusState) IsActive() bool {
	return s.Goal.Status == appserver.GoalActive
}

func (s GoalStatusState) Indicator(now time.Time, activeTurnStartedAt *time.Time) (GoalStatusIndicator, bool) {
	goal := s.Goal
	if goal.Status == appserver.GoalActive && activeTurnStartedAt != nil {
		baseline := s.ObservedAt
		if activeTurnStartedAt.After(baseline) {
			baseline = *activeTurnStartedAt
		}
		if now.After(baseline) {
			goal.TimeUsedSeconds += int64(now.Sub(baseline).Seconds())
		}
	}
	return GoalStatusIndicatorFromGoal(goal)
}

func GoalStatusIndicatorFromGoal(goal appserver.Goal) (GoalStatusIndicator, bool) {
	switch goal.Status {
	case appserver.GoalActive:
		return GoalStatusIndicator{Kind: GoalStatusIndicatorActive, Usage: ActiveGoalUsage(goal.TokenBudget, goal.TokensUsed, goal.TimeUsedSeconds)}, true
	case appserver.GoalPaused:
		return GoalStatusIndicator{Kind: GoalStatusIndicatorPaused}, true
	case appserver.GoalBlocked:
		return GoalStatusIndicator{Kind: GoalStatusIndicatorBlocked}, true
	case appserver.GoalUsageLimited:
		return GoalStatusIndicator{Kind: GoalStatusIndicatorUsageLimited}, true
	case appserver.GoalBudgetLimited:
		return GoalStatusIndicator{Kind: GoalStatusIndicatorBudgetLimited, Usage: StoppedGoalBudgetUsage(goal.TokenBudget, goal.TokensUsed)}, true
	case appserver.GoalComplete:
		return GoalStatusIndicator{Kind: GoalStatusIndicatorComplete, Usage: stringPtrChatwidget(CompletedGoalUsage(goal.TokenBudget, goal.TokensUsed, goal.TimeUsedSeconds))}, true
	default:
		return GoalStatusIndicator{}, false
	}
}

func ActiveGoalUsage(tokenBudget *int64, tokensUsed int64, timeUsedSeconds int64) *string {
	if tokenBudget != nil {
		return stringPtrChatwidget(FormatTokensCompact(tokensUsed) + " / " + FormatTokensCompact(*tokenBudget))
	}
	return stringPtrChatwidget(FormatGoalElapsedSeconds(timeUsedSeconds))
}

func StoppedGoalBudgetUsage(tokenBudget *int64, tokensUsed int64) *string {
	if tokenBudget == nil {
		return nil
	}
	return stringPtrChatwidget(FormatTokensCompact(tokensUsed) + " / " + FormatTokensCompact(*tokenBudget) + " tokens")
}

func CompletedGoalUsage(tokenBudget *int64, tokensUsed int64, timeUsedSeconds int64) string {
	if tokenBudget != nil {
		return FormatTokensCompact(tokensUsed) + " tokens"
	}
	return FormatGoalElapsedSeconds(timeUsedSeconds)
}

func FormatGoalElapsedSeconds(seconds int64) string {
	return FormatOptionalDuration(&seconds)
}

func GoalSummaryLines(goal appserver.Goal) []string {
	lines := []string{
		"Goal",
		"Status: " + GoalStatusLabel(goal.Status),
		"Objective: " + goal.Objective,
		"Time used: " + FormatGoalElapsedSeconds(goal.TimeUsedSeconds),
		"Tokens used: " + FormatTokensCompact(goal.TokensUsed),
	}
	if goal.TokenBudget != nil {
		lines = append(lines, "Token budget: "+FormatTokensCompact(*goal.TokenBudget))
	}
	lines = append(lines, "", GoalCommandHint(goal.Status))
	return lines
}

func GoalStatusLabel(status appserver.GoalStatus) string {
	switch status {
	case appserver.GoalActive:
		return "active"
	case appserver.GoalPaused:
		return "paused"
	case appserver.GoalBlocked:
		return "blocked"
	case appserver.GoalUsageLimited:
		return "usage limited"
	case appserver.GoalBudgetLimited:
		return "limited by budget"
	case appserver.GoalComplete:
		return "complete"
	default:
		return string(status)
	}
}

func GoalCommandHint(status appserver.GoalStatus) string {
	switch status {
	case appserver.GoalActive:
		return "Commands: /goal edit, /goal pause, /goal clear"
	case appserver.GoalPaused, appserver.GoalBlocked, appserver.GoalUsageLimited:
		return "Commands: /goal edit, /goal resume, /goal clear"
	case appserver.GoalBudgetLimited, appserver.GoalComplete:
		return "Commands: /goal edit, /goal clear"
	default:
		return "Commands: /goal edit, /goal clear"
	}
}

func EditedGoalStatus(status appserver.GoalStatus) appserver.GoalStatus {
	switch status {
	case appserver.GoalActive, appserver.GoalPaused, appserver.GoalBlocked, appserver.GoalUsageLimited:
		return status
	case appserver.GoalBudgetLimited, appserver.GoalComplete:
		return appserver.GoalActive
	default:
		return appserver.GoalActive
	}
}

func ResumePausedGoalView(objective string) SelectionView {
	return SelectionView{
		ViewID:               "resume-paused-goal",
		Title:                "Resume paused goal?",
		Subtitle:             "Goal: " + objective,
		FooterHint:           standardPopupHintLine,
		AllowCancel:          true,
		InitialSelectedIndex: 0,
		Items: []SelectionItem{
			{
				Name:            "Resume goal",
				Description:     "Mark it active and continue when idle",
				Action:          GoalMenuActionResume,
				DismissOnSelect: true,
			},
			{
				Name:            "Leave paused",
				Description:     "Keep it paused; use /goal resume later",
				DismissOnSelect: true,
			},
		},
	}
}

func FormatGoalStatusIndicator(indicator GoalStatusIndicator) string {
	label := string(indicator.Kind)
	if indicator.Usage != nil && *indicator.Usage != "" {
		return fmt.Sprintf("%s (%s)", label, *indicator.Usage)
	}
	return label
}

func stringPtrChatwidget(value string) *string {
	return &value
}
