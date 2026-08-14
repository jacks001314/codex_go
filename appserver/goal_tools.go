package appserver

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"codex_go/config"
	"codex_go/features"
	"codex_go/session"
	"codex_go/state"
	"codex_go/telemetry"
	"codex_go/tool"
	"codex_go/turn"
)

const (
	goalGetToolName    = "get_goal"
	goalCreateToolName = "create_goal"
	goalUpdateToolName = "update_goal"
)

type goalToolResponse struct {
	Goal                   *Goal   `json:"goal"`
	RemainingTokens        *int64  `json:"remainingTokens"`
	CompletionBudgetReport *string `json:"completionBudgetReport"`
}

func goalToolSpecForGet() tool.Spec {
	return tool.Spec{
		Name:        tool.PlainName(goalGetToolName),
		Description: "Get the current goal for this thread, including status, budgets, token and elapsed-time usage, and remaining token budget.",
		InputSchema: map[string]any{
			"type":                 "object",
			"properties":           map[string]any{},
			"additionalProperties": false,
		},
		Exposure: tool.ExposureModelVisible,
	}
}

func goalToolSpecForCreate() tool.Spec {
	return tool.Spec{
		Name: tool.PlainName(goalCreateToolName),
		Description: `Create a goal only when explicitly requested by the user or system/developer instructions; do not infer goals from ordinary tasks.
Set token_budget only when an explicit token budget is requested. Fails if an unfinished goal exists; use update_goal only for status.`,
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"objective": map[string]any{
					"type":        "string",
					"description": "Required. The concrete objective to start pursuing. This starts a new active goal when no goal exists or replaces the current goal when it is complete.",
				},
				"token_budget": map[string]any{
					"type":        "integer",
					"description": "Positive token budget for the new goal. Omit unless explicitly requested.",
				},
			},
			"required":             []string{"objective"},
			"additionalProperties": false,
		},
		Exposure: tool.ExposureModelVisible,
	}
}

func goalToolSpecForUpdate() tool.Spec {
	return tool.Spec{
		Name: tool.PlainName(goalUpdateToolName),
		Description: `Update the existing goal.
Use this tool only to mark the goal achieved or genuinely blocked.
Set status to complete only when the objective has actually been achieved and no required work remains.
Set status to blocked only when the same blocking condition has repeated for at least three consecutive goal turns, counting the original/user-triggered turn and any automatic continuations, and the agent cannot make meaningful progress without user input or an external-state change.
If the user resumes a goal that was previously marked blocked, treat the resumed run as a fresh blocked audit. If the same blocking condition then repeats for at least three consecutive resumed goal turns, set status to blocked again.
Once the blocked threshold is satisfied, do not keep reporting that you are still blocked while leaving the goal active; set status to blocked.
Do not use blocked merely because the work is hard, slow, uncertain, incomplete, or would benefit from clarification.
Do not mark a goal complete merely because its budget is nearly exhausted or because you are stopping work.
You cannot use this tool to pause, resume, budget-limit, or usage-limit a goal; those status changes are controlled by the user or system.
When marking a budgeted goal achieved with status complete, report the final token usage from the tool result to the user.`,
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"status": map[string]any{
					"type":        "string",
					"enum":        []string{"complete", "blocked"},
					"description": "Required. Set to `complete` only when the objective is achieved and no required work remains. Set to `blocked` only after the same blocking condition has recurred for at least three consecutive goal turns and the agent is at an impasse. After a previously blocked goal is resumed, the resumed run starts a fresh blocked audit.",
				},
			},
			"required":             []string{"status"},
			"additionalProperties": false,
		},
		Exposure: tool.ExposureModelVisible,
	}
}

func (r *RuntimeRouter) goalToolExecutorsForTurn(cfg *config.Config, threadID, turnID string) []tool.Executor {
	if r == nil || cfg == nil || !features.Enabled(cfg.FeatureSettings(), "goals") {
		return nil
	}
	if r.services.StateRuntime == nil || r.services.ThreadRouter == nil || r.services.ThreadRouter.store == nil {
		return nil
	}
	if _, ok := r.ephemeralThreadRecord(session.ThreadID(threadID), false); ok {
		return nil
	}
	record, err := r.threadRecord(session.ThreadID(threadID), true, false)
	if err != nil || record == nil {
		return nil
	}
	if strings.EqualFold(strings.TrimSpace(record.Metadata.ThreadSource), string(ThreadSourceKindSubAgentReview)) {
		return nil
	}

	var maxGoalTokenBudget *int64
	if goals, goalsErr := cfg.GoalsConfig(); goalsErr == nil && goals != nil {
		maxGoalTokenBudget = cloneInt64PtrAppserver(goals.MaxGoalTokenBudget)
	}
	return []tool.Executor{
		tool.NewExecutorFunc(goalToolSpecForGet(), func(ctx context.Context, invocation *tool.Invocation) (*tool.Output, error) {
			return r.executeGoalToolGet(ctx, invocation, threadID)
		}),
		tool.NewExecutorFunc(goalToolSpecForCreate(), func(ctx context.Context, invocation *tool.Invocation) (*tool.Output, error) {
			return r.executeGoalToolCreate(ctx, invocation, threadID, turnID, maxGoalTokenBudget)
		}),
		tool.NewExecutorFunc(goalToolSpecForUpdate(), func(ctx context.Context, invocation *tool.Invocation) (*tool.Output, error) {
			return r.executeGoalToolUpdate(ctx, invocation, threadID, turnID)
		}),
	}
}

func (r *RuntimeRouter) executeGoalToolGet(ctx context.Context, invocation *tool.Invocation, threadID string) (*tool.Output, error) {
	goal, err := r.services.StateRuntime.GetThreadGoal(ctx, threadID)
	if err != nil {
		return nil, tool.RespondToModel("failed to read goal: " + err.Error())
	}
	return goalToolOutput(goalToolResponseForState(goal, false)), nil
}

func (r *RuntimeRouter) executeGoalToolCreate(ctx context.Context, invocation *tool.Invocation, threadID, turnID string, maxGoalTokenBudget *int64) (*tool.Output, error) {
	var args struct {
		Objective   string `json:"objective"`
		TokenBudget *int64 `json:"token_budget"`
	}
	if err := invocation.DecodeArguments(&args); err != nil {
		return nil, tool.RespondToModel(err.Error())
	}
	objective := strings.TrimSpace(args.Objective)
	if objective == "" {
		return nil, tool.RespondToModel("goal objective must be non-empty")
	}
	if len([]rune(objective)) > 4000 {
		return nil, tool.RespondToModel("goal objective must be at most 4000 characters")
	}
	tokenBudget := args.TokenBudget
	if tokenBudget == nil {
		tokenBudget = cloneInt64PtrAppserver(maxGoalTokenBudget)
	}
	if err := validateGoalBudgetAgainstMax(tokenBudget, maxGoalTokenBudget); err != nil {
		return nil, tool.RespondToModel(err.Error())
	}
	goal, err := r.services.StateRuntime.InsertThreadGoal(ctx, threadID, objective, state.ThreadGoalActive, tokenBudget)
	if err != nil {
		return nil, tool.RespondToModel("failed to create goal: " + err.Error())
	}
	if goal == nil {
		return nil, tool.RespondToModel("cannot create a new goal because this thread has an unfinished goal; complete the existing goal first")
	}
	if _, err := r.services.StateRuntime.SetThreadPreviewIfEmpty(ctx, threadID, goal.Objective); err != nil {
		slog.Warn("failed to set empty thread preview from goal objective", "thread_id", threadID, "error", err)
	}
	r.markStateThreadGoalTurnActiveNow(threadID, turnID, goal.GoalID)
	r.emitStateThreadGoalUpdate(goal, turnID, "", telemetry.GoalEventKindCreated)
	return goalToolOutput(goalToolResponseForState(goal, false)), nil
}

func (r *RuntimeRouter) executeGoalToolUpdate(ctx context.Context, invocation *tool.Invocation, threadID, turnID string) (*tool.Output, error) {
	var args struct {
		Status string `json:"status"`
	}
	if err := invocation.DecodeArguments(&args); err != nil {
		return nil, tool.RespondToModel(err.Error())
	}
	status := state.ThreadGoalStatus(strings.TrimSpace(args.Status))
	var mode state.GoalAccountingMode
	switch status {
	case state.ThreadGoalComplete:
		mode = state.GoalAccountingActiveOrComplete
	case state.ThreadGoalBlocked:
		mode = state.GoalAccountingActiveOrStopped
	default:
		return nil, tool.RespondToModel("update_goal can only mark the existing goal complete or blocked; pause, resume, budget-limited, and usage-limited status changes are controlled by the user or system")
	}

	r.accountStateThreadGoalProgress(threadID, turnID, time.Now().UTC(), mode)
	expectedGoalID := r.stateThreadGoalTurnExpectedID(threadID, turnID)
	goal, err := r.services.StateRuntime.UpdateThreadGoal(ctx, threadID, state.GoalUpdate{
		Status:         &status,
		ExpectedGoalID: expectedGoalID,
	})
	if err != nil {
		return nil, tool.RespondToModel("failed to update goal: " + err.Error())
	}
	if goal == nil {
		return nil, tool.RespondToModel("cannot update goal because this thread has no goal")
	}
	if status == state.ThreadGoalComplete {
		r.setStateThreadGoalTurnFinishMode(threadID, turnID, state.GoalAccountingActiveOrComplete)
	} else {
		r.clearStateThreadGoalTurnSnapshot(threadID, turnID)
	}
	r.emitStateThreadGoalUpdate(goal, turnID, "", telemetry.GoalEventKindStatusChanged)
	return goalToolOutput(goalToolResponseForState(goal, status == state.ThreadGoalComplete)), nil
}

func goalToolResponseForState(goal *state.ThreadGoal, includeCompletionReport bool) goalToolResponse {
	apiGoal := apiGoalFromState(goal)
	response := goalToolResponse{Goal: apiGoal}
	if apiGoal != nil && apiGoal.TokenBudget != nil {
		remaining := *apiGoal.TokenBudget - apiGoal.TokensUsed
		if remaining < 0 {
			remaining = 0
		}
		response.RemainingTokens = &remaining
	}
	if includeCompletionReport && apiGoal != nil && apiGoal.Status == GoalComplete {
		if apiGoal.TokenBudget != nil || apiGoal.TimeUsedSeconds > 0 {
			report := "Goal achieved. Report final usage from this tool result's structured goal fields. If `goal.tokenBudget` is present, include token usage from `goal.tokensUsed` and `goal.tokenBudget`. If `goal.timeUsedSeconds` is greater than 0, summarize elapsed time in a concise, human-friendly form appropriate to the response language."
			response.CompletionBudgetReport = &report
		}
	}
	return response
}

func goalToolOutput(value any) *tool.Output {
	return &tool.Output{Success: true, Data: map[string]any{"output": value}}
}

func (r *RuntimeRouter) accountGoalToolProgressForCompletion(threadID, turnID string, execution *turn.ToolExecutionResult) {
	if r == nil || execution == nil || execution.Invocation == nil || execution.Output == nil {
		return
	}
	if execution.Invocation.ToolName.Namespace == "" && execution.Invocation.ToolName.Name == goalUpdateToolName {
		return
	}
	r.accountStateThreadGoalProgress(threadID, turnID, execution.FinishedAt, state.GoalAccountingActiveOnly)
}
