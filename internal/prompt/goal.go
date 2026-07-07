package prompt

import (
	"strconv"
	"strings"
)

type Goal struct {
	Objective       string
	TokenBudget     *int64
	TokensUsed      int64
	TimeUsedSeconds int64
}

func Continuation(goal *Goal) string {
	if goal == nil {
		goal = &Goal{}
	}
	return strings.Join([]string{
		"<goal_continuation>",
		"<objective>" + escapeXMLText(goal.Objective) + "</objective>",
		"<tokens_used>" + strconv.FormatInt(goal.TokensUsed, 10) + "</tokens_used>",
		"<token_budget>" + tokenBudget(goal.TokenBudget) + "</token_budget>",
		"<remaining_tokens>" + remainingTokens(goal.TokenBudget, goal.TokensUsed) + "</remaining_tokens>",
		"Continue working on the active goal. Keep progress within the remaining budget when one exists.",
		"</goal_continuation>",
	}, "\n")
}

func BudgetLimit(goal *Goal) string {
	if goal == nil {
		goal = &Goal{}
	}
	return strings.Join([]string{
		"<goal_budget_limit>",
		"<objective>" + escapeXMLText(goal.Objective) + "</objective>",
		"<tokens_used>" + strconv.FormatInt(goal.TokensUsed, 10) + "</tokens_used>",
		"<time_used_seconds>" + strconv.FormatInt(goal.TimeUsedSeconds, 10) + "</time_used_seconds>",
		"<token_budget>" + tokenBudget(goal.TokenBudget) + "</token_budget>",
		"Wrap up with the most useful current result and clearly state anything still incomplete.",
		"</goal_budget_limit>",
	}, "\n")
}

func ObjectiveUpdated(goal *Goal) string {
	if goal == nil {
		goal = &Goal{}
	}
	return strings.Join([]string{
		"<goal_objective_updated>",
		"<objective>" + escapeXMLText(goal.Objective) + "</objective>",
		"<tokens_used>" + strconv.FormatInt(goal.TokensUsed, 10) + "</tokens_used>",
		"<token_budget>" + tokenBudget(goal.TokenBudget) + "</token_budget>",
		"<remaining_tokens>" + remainingTokens(goal.TokenBudget, goal.TokensUsed) + "</remaining_tokens>",
		"Use the updated objective for subsequent work.",
		"</goal_objective_updated>",
	}, "\n")
}

func tokenBudget(value *int64) string {
	if value == nil {
		return "none"
	}
	return strconv.FormatInt(*value, 10)
}

func remainingTokens(budget *int64, used int64) string {
	if budget == nil {
		return "unbounded"
	}
	remaining := *budget - used
	if remaining < 0 {
		remaining = 0
	}
	return strconv.FormatInt(remaining, 10)
}

func escapeXMLText(input string) string {
	replacer := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")
	return replacer.Replace(input)
}
