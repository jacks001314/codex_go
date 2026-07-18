package chatwidget

import (
	"fmt"
	"strconv"
	"strings"
)

const (
	NudgeModelSlug                 = "gpt-5.4-mini"
	RateLimitSwitchPromptViewID    = "rate-limit-switch-prompt"
	RateLimitSwitchPromptThreshold = 90.0
	primaryLimitFallbackLabel      = "usage"
	secondaryLimitFallbackLabel    = "secondary usage"
)

var rateLimitWarningThresholds = []float64{75, 90, 95}

const (
	UsageMenuActionRateLimitSwitchModel  UsageMenuAction = "rate_limit_switch_model"
	UsageMenuActionRateLimitKeepModel    UsageMenuAction = "rate_limit_keep_model"
	UsageMenuActionRateLimitHideNudge    UsageMenuAction = "rate_limit_hide_model_nudge"
	UsageMenuActionAddCreditsNudgeSend   UsageMenuAction = "add_credits_nudge_send"
	UsageMenuActionAddCreditsNudgeCancel UsageMenuAction = "add_credits_nudge_cancel"
)

type RateLimitSwitchPromptState string

const (
	RateLimitSwitchPromptIdle    RateLimitSwitchPromptState = "idle"
	RateLimitSwitchPromptPending RateLimitSwitchPromptState = "pending"
	RateLimitSwitchPromptShown   RateLimitSwitchPromptState = "shown"
)

type RateLimitWindow struct {
	UsedPercent        float64
	WindowDurationMins *int64
}

type RateLimitCredits struct {
	HasCredits bool
	Unlimited  bool
	Balance    *string
}

type RateLimitSnapshot struct {
	LimitID   string
	Primary   *RateLimitWindow
	Secondary *RateLimitWindow
	Credits   *RateLimitCredits
	PlanType  string
}

type RateLimitWarningState struct {
	secondaryIndex int
	primaryIndex   int
}

type RateLimitSwitchPreset struct {
	Model                  string
	Description            string
	DefaultReasoningEffort string
}

type AddCreditsNudgeEmailStatus string

const (
	AddCreditsNudgeEmailSent           AddCreditsNudgeEmailStatus = "sent"
	AddCreditsNudgeEmailCooldownActive AddCreditsNudgeEmailStatus = "cooldown_active"
)

type AddCreditsNudgeEmailState struct {
	InFlight *AddCreditsNudgeCreditType
}

func (s *RateLimitWarningState) TakeWarnings(snapshot RateLimitSnapshot) []string {
	if s == nil || !isCodexLimit(snapshot.LimitID) || hasWorkspaceCredits(snapshot.Credits) {
		return nil
	}
	secondaryPercent, secondaryWindow := rateLimitWindowValues(snapshot.Secondary)
	primaryPercent, primaryWindow := rateLimitWindowValues(snapshot.Primary)
	if secondaryPercent != nil && *secondaryPercent == 100 || primaryPercent != nil && *primaryPercent == 100 {
		return nil
	}

	warnings := []string{}
	warnings = append(warnings, s.takeWindowWarning(
		&s.secondaryIndex,
		secondaryPercent,
		secondaryWindow,
		true,
	)...)
	warnings = append(warnings, s.takeWindowWarning(
		&s.primaryIndex,
		primaryPercent,
		primaryWindow,
		false,
	)...)
	return warnings
}

func ShouldQueueRateLimitSwitchPrompt(snapshot RateLimitSnapshot, currentModel string, hidden bool, state RateLimitSwitchPromptState) bool {
	if hidden || state == RateLimitSwitchPromptShown || !isCodexLimit(snapshot.LimitID) || hasWorkspaceCredits(snapshot.Credits) {
		return false
	}
	if strings.EqualFold(strings.TrimSpace(currentModel), NudgeModelSlug) {
		return false
	}
	return rateLimitWindowHighUsage(snapshot.Primary) || rateLimitWindowHighUsage(snapshot.Secondary)
}

func NewRateLimitSwitchPromptView(preset RateLimitSwitchPreset) SelectionView {
	model := strings.TrimSpace(preset.Model)
	if model == "" {
		model = NudgeModelSlug
	}
	description := strings.TrimSpace(preset.Description)
	if description == "" {
		description = "Uses fewer credits for upcoming turns."
	}
	return SelectionView{
		ViewID:      RateLimitSwitchPromptViewID,
		Title:       "Approaching rate limits",
		Subtitle:    "Switch to " + model + " for lower credit usage?",
		FooterHint:  standardPopupHintLine,
		AllowCancel: true,
		Items: []SelectionItem{
			{
				ID:              "switch",
				Name:            "Switch to " + model,
				Description:     description,
				Action:          UsageMenuActionRateLimitSwitchModel,
				DismissOnSelect: true,
			},
			{
				ID:              "keep",
				Name:            "Keep current model",
				Action:          UsageMenuActionRateLimitKeepModel,
				DismissOnSelect: true,
			},
			{
				ID:              "hide",
				Name:            "Keep current model (never show again)",
				Description:     "Hide future rate limit reminders about switching models.",
				Action:          UsageMenuActionRateLimitHideNudge,
				DismissOnSelect: true,
			},
		},
	}
}

func NewWorkspaceOwnerNudgePromptView(creditType AddCreditsNudgeCreditType) SelectionView {
	title, prompt := workspaceOwnerNudgeCopy(creditType)
	return SelectionView{
		Title:                title,
		Subtitle:             prompt,
		FooterHint:           standardPopupHintLine,
		AllowCancel:          true,
		InitialSelectedIndex: 1,
		Items: []SelectionItem{
			{
				ID:              "yes",
				Name:            "Yes",
				Action:          UsageMenuActionAddCreditsNudgeSend,
				DismissOnSelect: true,
			},
			{
				ID:              "no",
				Name:            "No",
				IsDefault:       true,
				Action:          UsageMenuActionAddCreditsNudgeCancel,
				DismissOnSelect: true,
			},
		},
	}
}

func (s *AddCreditsNudgeEmailState) CanOpenWorkspaceOwnerNudgePrompt() bool {
	return s == nil || s.InFlight == nil
}

func (s *AddCreditsNudgeEmailState) StartAddCreditsNudgeEmailRequest(creditType AddCreditsNudgeCreditType) bool {
	if s == nil {
		return false
	}
	value := creditType
	s.InFlight = &value
	return true
}

func (s *AddCreditsNudgeEmailState) FinishAddCreditsNudgeEmailRequest(status AddCreditsNudgeEmailStatus, failed bool) string {
	creditType := AddCreditsNudgeCredits
	if s != nil && s.InFlight != nil {
		creditType = *s.InFlight
		s.InFlight = nil
	}
	switch {
	case creditType == AddCreditsNudgeCredits && !failed && status == AddCreditsNudgeEmailSent:
		return "Workspace owner notified."
	case creditType == AddCreditsNudgeCredits && !failed && status == AddCreditsNudgeEmailCooldownActive:
		return "Workspace owner was already notified recently."
	case creditType == AddCreditsNudgeCredits:
		return "Could not notify your workspace owner. Please try again."
	case creditType == AddCreditsNudgeUsageLimit && !failed && status == AddCreditsNudgeEmailSent:
		return "Limit increase requested."
	case creditType == AddCreditsNudgeUsageLimit && !failed && status == AddCreditsNudgeEmailCooldownActive:
		return "A limit increase was already requested recently."
	default:
		return "Could not request a limit increase. Please try again."
	}
}

func workspaceOwnerNudgeCopy(creditType AddCreditsNudgeCreditType) (string, string) {
	switch creditType {
	case AddCreditsNudgeUsageLimit:
		return "Usage limit reached", "Request a limit increase from your owner to continue using codex. Request increase?"
	default:
		return "You've reached your workspace credit limit", "Your workspace is out of credits. Ask your workspace owner to add more. Notify owner?"
	}
}

func (s *RateLimitWarningState) takeWindowWarning(index *int, usedPercent *float64, windowMinutes *int64, secondary bool) []string {
	if usedPercent == nil || index == nil {
		return nil
	}
	var highest *float64
	for *index < len(rateLimitWarningThresholds) && *usedPercent >= rateLimitWarningThresholds[*index] {
		threshold := rateLimitWarningThresholds[*index]
		highest = &threshold
		*index = *index + 1
	}
	if highest == nil {
		return nil
	}
	remaining := 100 - *highest
	label := LimitLabelForWindow(windowMinutes, secondary)
	return []string{fmt.Sprintf("Heads up, you have less than %.0f%% of your %s limit left. Run /status for a breakdown.", remaining, label)}
}

func LimitLabelForWindow(windowMinutes *int64, secondary bool) string {
	if windowMinutes != nil {
		if label, ok := LimitsDuration(*windowMinutes); ok {
			return label
		}
	}
	if secondary {
		return secondaryLimitFallbackLabel
	}
	return primaryLimitFallbackLabel
}

func LimitsDuration(minutes int64) (string, bool) {
	const minutesPerHour int64 = 60
	const minutesPer5Hours = 5 * minutesPerHour
	const minutesPerDay = 24 * minutesPerHour
	const minutesPerWeek = 7 * minutesPerDay
	const minutesPerMonth = 30 * minutesPerDay
	const minutesPerYear = 365 * minutesPerDay

	if minutes < 0 {
		minutes = 0
	}
	switch {
	case isApproximateWindow(minutes, minutesPer5Hours):
		return "5h", true
	case isApproximateWindow(minutes, minutesPerDay):
		return "daily", true
	case isApproximateWindow(minutes, minutesPerWeek):
		return "weekly", true
	case isApproximateWindow(minutes, minutesPerMonth):
		return "monthly", true
	case isApproximateWindow(minutes, minutesPerYear):
		return "annual", true
	default:
		return "", false
	}
}

func isApproximateWindow(minutes int64, expected int64) bool {
	value := float64(minutes)
	target := float64(expected)
	return value >= target*0.95 && value <= target*1.05
}

func rateLimitWindowValues(window *RateLimitWindow) (*float64, *int64) {
	if window == nil {
		return nil, nil
	}
	return &window.UsedPercent, window.WindowDurationMins
}

func rateLimitWindowHighUsage(window *RateLimitWindow) bool {
	return window != nil && window.UsedPercent >= RateLimitSwitchPromptThreshold
}

func isCodexLimit(limitID string) bool {
	limitID = strings.TrimSpace(limitID)
	return limitID == "" || strings.EqualFold(limitID, "codex")
}

func hasWorkspaceCredits(credits *RateLimitCredits) bool {
	if credits == nil || !credits.HasCredits {
		return false
	}
	if credits.Unlimited {
		return true
	}
	if credits.Balance == nil {
		return false
	}
	value, err := strconv.ParseFloat(strings.TrimSpace(*credits.Balance), 64)
	return err == nil && value > 0
}
