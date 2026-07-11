package app

import (
	"errors"
	"fmt"
	"strings"

	"codex_go/internal/appserver"
	codextui "codex_go/internal/tui"
	"codex_go/internal/tui/chatwidget"
)

// Rust parity subset: codex-rs/tui/src/app/thread_goal_actions.rs.

const EphemeralThreadGoalErrorMessage = "Goals need a saved session. This session is temporary.\n" +
	"Run `codex` to start a saved session, or `codex resume` / `/resume` to reopen one."

type ThreadGoalAction struct {
	ThreadID string
	Action   string
}

type ThreadGoalSetMode string

const (
	ThreadGoalSetConfirmIfExists ThreadGoalSetMode = "confirm_if_exists"
	ThreadGoalSetReplaceExisting ThreadGoalSetMode = "replace_existing"
	ThreadGoalSetUpdateExisting  ThreadGoalSetMode = "update_existing"

	ThreadGoalUsageMessage = "Usage: /goal [<objective>|clear|edit|pause|resume]"
)

type ThreadGoalReadDecision struct {
	Ignore                 bool
	ErrorMessage           string
	ShowUsage              bool
	UsageHint              string
	ShowSummary            bool
	ShowEditPrompt         bool
	ShowResumePausedPrompt bool
	Goal                   *appserver.Goal
}

type ThreadGoalSetPreflightDecision struct {
	Ignore                       bool
	ErrorMessage                 string
	ShowReplaceConfirmation      bool
	Mode                         ThreadGoalSetMode
	ReplacingGoal                bool
	ClearExistingBeforeSet       bool
	Status                       appserver.GoalStatus
	TokenBudget                  *int64
	CleanupMaterializedGoalFiles bool
}

type ThreadGoalActionResultDecision struct {
	Ignore                       bool
	InfoMessage                  string
	Hint                         string
	ErrorMessage                 string
	MaybeSendQueuedInput         bool
	CleanupMaterializedGoalFiles bool
}

type ReplaceThreadGoalConfirmationView struct {
	Title           string
	Subtitle        string
	FooterHint      string
	ReplaceName     string
	ReplaceHint     string
	CancelName      string
	CancelHint      string
	ThreadID        string
	Objective       string
	ReplacementMode ThreadGoalSetMode
}

func ThreadGoalErrorMessage(action string, err error) string {
	if IsEphemeralThreadGoalError(err) {
		return EphemeralThreadGoalErrorMessage
	}
	return fmt.Sprintf("Failed to %s thread goal: %v", action, err)
}

func OpenThreadGoalMenuDecision(displayedThreadMatches bool, goal *appserver.Goal, readErr error) ThreadGoalReadDecision {
	if !displayedThreadMatches {
		return ThreadGoalReadDecision{Ignore: true}
	}
	if readErr != nil {
		return ThreadGoalReadDecision{ErrorMessage: ThreadGoalErrorMessage("read", readErr)}
	}
	if goal == nil {
		return ThreadGoalReadDecision{ShowUsage: true, UsageHint: "No goal is currently set."}
	}
	cloned := *goal
	return ThreadGoalReadDecision{ShowSummary: true, Goal: &cloned}
}

func ResumePausedGoalPromptDecision(displayedThreadMatches bool, goal *appserver.Goal, readErr error) ThreadGoalReadDecision {
	if !displayedThreadMatches || readErr != nil || goal == nil || !GoalStatusShouldPromptResumeAfterResume(goal.Status) {
		return ThreadGoalReadDecision{Ignore: !displayedThreadMatches}
	}
	cloned := *goal
	return ThreadGoalReadDecision{ShowResumePausedPrompt: true, Goal: &cloned}
}

func OpenThreadGoalEditorDecision(displayedThreadMatches bool, hasThreadID bool, goal *appserver.Goal, readErr error) ThreadGoalReadDecision {
	if !hasThreadID {
		return NoThreadGoalToEditDecision()
	}
	if !displayedThreadMatches {
		return ThreadGoalReadDecision{Ignore: true}
	}
	if readErr != nil {
		return ThreadGoalReadDecision{ErrorMessage: ThreadGoalErrorMessage("read", readErr)}
	}
	if goal == nil {
		return NoThreadGoalToEditDecision()
	}
	cloned := *goal
	return ThreadGoalReadDecision{ShowEditPrompt: true, Goal: &cloned}
}

func NoThreadGoalToEditDecision() ThreadGoalReadDecision {
	return ThreadGoalReadDecision{
		ErrorMessage: "No goal is currently set.",
		ShowUsage:    true,
		UsageHint:    "Create a goal before editing it.",
	}
}

func SetThreadGoalDraftPreflightDecision(displayedThreadMatches bool, existingGoal *appserver.Goal, readErr error, mode ThreadGoalSetMode, updateStatus appserver.GoalStatus, tokenBudget *int64) ThreadGoalSetPreflightDecision {
	if !displayedThreadMatches {
		return ThreadGoalSetPreflightDecision{Ignore: true}
	}
	if readErr != nil {
		return ThreadGoalSetPreflightDecision{ErrorMessage: ThreadGoalErrorMessage("read", readErr)}
	}
	if mode == "" {
		mode = ThreadGoalSetConfirmIfExists
	}
	if mode == ThreadGoalSetConfirmIfExists {
		if ShouldConfirmBeforeReplacingGoal(existingGoal) {
			return ThreadGoalSetPreflightDecision{ShowReplaceConfirmation: true, Mode: mode}
		}
		if existingGoal != nil {
			mode = ThreadGoalSetReplaceExisting
		}
	}
	decision := ThreadGoalSetPreflightDecision{Mode: mode}
	switch mode {
	case ThreadGoalSetReplaceExisting:
		decision.ReplacingGoal = true
		decision.ClearExistingBeforeSet = true
		decision.Status = appserver.GoalActive
	case ThreadGoalSetUpdateExisting:
		decision.Status = updateStatus
		decision.TokenBudget = cloneInt64PointerGoal(tokenBudget)
	default:
		decision.Status = appserver.GoalActive
	}
	return decision
}

func ThreadGoalSetSuccessDecision(displayedThreadMatches bool, goal appserver.Goal) ThreadGoalActionResultDecision {
	if !displayedThreadMatches {
		return ThreadGoalActionResultDecision{Ignore: true}
	}
	return ThreadGoalActionResultDecision{
		InfoMessage:          "Goal " + GoalStatusLabelForThreadGoal(goal.Status),
		Hint:                 GoalUsageSummaryForThreadGoal(goal),
		MaybeSendQueuedInput: true,
	}
}

func ThreadGoalSetErrorDecision(displayedThreadMatches bool, replacingGoal bool, err error) ThreadGoalActionResultDecision {
	decision := ThreadGoalActionResultDecision{CleanupMaterializedGoalFiles: true}
	if !displayedThreadMatches {
		decision.Ignore = true
		return decision
	}
	action := "set"
	if replacingGoal {
		action = "replace"
	}
	decision.ErrorMessage = ThreadGoalErrorMessage(action, err)
	return decision
}

func ThreadGoalStatusUpdateDecision(displayedThreadMatches bool, goal *appserver.Goal, err error) ThreadGoalActionResultDecision {
	if !displayedThreadMatches {
		return ThreadGoalActionResultDecision{Ignore: true}
	}
	if err != nil {
		return ThreadGoalActionResultDecision{ErrorMessage: ThreadGoalErrorMessage("update", err)}
	}
	if goal == nil {
		return ThreadGoalActionResultDecision{}
	}
	return ThreadGoalActionResultDecision{
		InfoMessage: "Goal " + GoalStatusLabelForThreadGoal(goal.Status),
		Hint:        GoalUsageSummaryForThreadGoal(*goal),
	}
}

func ThreadGoalClearDecision(displayedThreadMatches bool, cleared bool, err error) ThreadGoalActionResultDecision {
	if !displayedThreadMatches {
		return ThreadGoalActionResultDecision{Ignore: true}
	}
	if err != nil {
		return ThreadGoalActionResultDecision{ErrorMessage: ThreadGoalErrorMessage("clear", err)}
	}
	if cleared {
		return ThreadGoalActionResultDecision{InfoMessage: "Goal cleared"}
	}
	return ThreadGoalActionResultDecision{
		InfoMessage: "No goal to clear",
		Hint:        "This thread does not currently have a goal.",
	}
}

func ReplaceThreadGoalConfirmation(threadID string, objective string) ReplaceThreadGoalConfirmationView {
	return ReplaceThreadGoalConfirmationView{
		Title:           "Replace goal?",
		Subtitle:        "New objective: " + TruncateGoalObjective(objective, 200),
		FooterHint:      "up/down navigate | enter select | esc close",
		ReplaceName:     "Replace current goal",
		ReplaceHint:     "Set the new objective and start it now",
		CancelName:      "Cancel",
		CancelHint:      "Keep the current goal",
		ThreadID:        threadID,
		Objective:       objective,
		ReplacementMode: ThreadGoalSetReplaceExisting,
	}
}

func TruncateGoalObjective(objective string, maxGraphemes int) string {
	return codextui.TruncateText(objective, maxGraphemes)
}

func IsEphemeralThreadGoalError(err error) bool {
	return errorChainContainsAny(err,
		"ephemeral thread does not support goals",
		"thread goals require a persisted thread; this thread is ephemeral",
	)
}

func ShouldConfirmBeforeReplacingGoal(goal *appserver.Goal) bool {
	if goal == nil {
		return false
	}
	switch goal.Status {
	case appserver.GoalComplete:
		return false
	case appserver.GoalActive,
		appserver.GoalPaused,
		appserver.GoalBlocked,
		appserver.GoalUsageLimited,
		appserver.GoalBudgetLimited:
		return true
	default:
		return false
	}
}

func GoalStatusLabelForThreadGoal(status appserver.GoalStatus) string {
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

func GoalUsageSummaryForThreadGoal(goal appserver.Goal) string {
	parts := []string{"Objective: " + goal.Objective}
	if goal.TimeUsedSeconds > 0 {
		parts = append(parts, "Time: "+chatwidget.FormatGoalElapsedSeconds(goal.TimeUsedSeconds)+".")
	}
	if goal.TokenBudget != nil {
		parts = append(parts, "Tokens: "+chatwidget.FormatTokensCompact(goal.TokensUsed)+"/"+chatwidget.FormatTokensCompact(*goal.TokenBudget)+".")
	}
	return strings.Join(parts, " ")
}

func GoalStatusShouldPromptResumeAfterResume(status appserver.GoalStatus) bool {
	switch status {
	case appserver.GoalPaused, appserver.GoalBlocked, appserver.GoalUsageLimited:
		return true
	default:
		return false
	}
}

func cloneInt64PointerGoal(value *int64) *int64 {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func errorChainContainsAny(err error, needles ...string) bool {
	if err == nil {
		return false
	}
	for _, needle := range needles {
		if strings.Contains(err.Error(), needle) {
			return true
		}
	}
	if multi, ok := err.(interface{ Unwrap() []error }); ok {
		for _, inner := range multi.Unwrap() {
			if errorChainContainsAny(inner, needles...) {
				return true
			}
		}
		return false
	}
	unwrapped := errors.Unwrap(err)
	return unwrapped != nil && errorChainContainsAny(unwrapped, needles...)
}
