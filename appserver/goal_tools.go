package appserver

import (
	"context"
	"encoding/json"
	"fmt"
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

type goalToolResponse struct {
	Goal                   *Goal   `json:"goal"`
	RemainingTokens        *int64  `json:"remainingTokens"`
	CompletionBudgetReport *string `json:"completionBudgetReport"`
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
		tool.NewExecutorFunc(tool.GoalGetToolSpec(), func(ctx context.Context, invocation *tool.Invocation) (*tool.Output, error) {
			return r.executeGoalToolGet(ctx, invocation, threadID)
		}),
		tool.NewExecutorFunc(tool.GoalCreateToolSpec(), func(ctx context.Context, invocation *tool.Invocation) (*tool.Output, error) {
			return r.executeGoalToolCreate(ctx, invocation, threadID, turnID, maxGoalTokenBudget)
		}),
		tool.NewExecutorFunc(tool.GoalUpdateToolSpec(), func(ctx context.Context, invocation *tool.Invocation) (*tool.Output, error) {
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
	return &tool.Output{
		Success: true,
		// The Responses API requires function_call_output.output to be a
		// string or a list of output content parts. Structured tool results
		// (like the goal snapshot) must therefore be serialized to text for
		// the wire; the structured value stays in Data for internal consumers.
		Body: goalToolOutputBodyText(value),
		Data: map[string]any{"output": value},
	}
}

func goalToolOutputBodyText(value any) string {
	if text, ok := value.(string); ok {
		return text
	}
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Sprint(value)
	}
	return string(data)
}

func (r *RuntimeRouter) accountGoalToolProgressForCompletion(threadID, turnID string, execution *turn.ToolExecutionResult) {
	if r == nil || execution == nil || execution.Invocation == nil || execution.Output == nil {
		return
	}
	if execution.Invocation.ToolName.Namespace == "" && execution.Invocation.ToolName.Name == tool.GoalUpdateToolName {
		return
	}
	r.accountStateThreadGoalProgress(threadID, turnID, execution.FinishedAt, state.GoalAccountingActiveOnly)
}
