package exec

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"codex_go/features"
	"codex_go/session"
	"codex_go/tool"

	"github.com/google/uuid"
)

// execThreadGoalExtraKey mirrors appserver's thread_goal record key so the
// exec runner's goal tools share storage with the local TUI goal callbacks.
const execThreadGoalExtraKey = "thread_goal"

type execGoal struct {
	ThreadID        string `json:"threadId"`
	GoalID          string `json:"goalId,omitempty"`
	Objective       string `json:"objective"`
	TokenBudget     *int64 `json:"tokenBudget,omitempty"`
	TokensUsed      int64  `json:"tokensUsed"`
	TimeUsedSeconds int64  `json:"timeUsedSeconds"`
	Status          string `json:"status"`
	CreatedAt       int64  `json:"createdAt"`
	UpdatedAt       int64  `json:"updatedAt"`
}

type execGoalToolResponse struct {
	Goal                   *execGoal `json:"goal"`
	RemainingTokens        *int64    `json:"remainingTokens"`
	CompletionBudgetReport *string   `json:"completionBudgetReport"`
}

type execGoalToolHandler struct {
	runner             *Runner
	store              *session.Store
	maxGoalTokenBudget *int64
}

// goalToolExecutorsForRequest returns model-visible goal tools for a turn when
// the goals feature is enabled and the thread is a normal (non-review,
// non-ephemeral) session, mirroring the app-server's goal tool registration.
func (r *Runner) goalToolExecutorsForRequest(req *Request, run *agentRunConfig) []tool.Executor {
	if r == nil || run == nil || run.Config == nil || !features.Enabled(run.Config.FeatureSettings(), "goals") {
		return nil
	}
	if req == nil || req.Exec.Ephemeral || req.subagent != nil || strings.EqualFold(strings.TrimSpace(req.Exec.Subcommand), "review") {
		return nil
	}
	threadID := strings.TrimSpace(run.ThreadID)
	turnID := strings.TrimSpace(run.TurnID)
	if threadID == "" || turnID == "" {
		return nil
	}
	var maxGoalTokenBudget *int64
	if goals, goalsErr := run.Config.GoalsConfig(); goalsErr == nil && goals != nil {
		maxGoalTokenBudget = goals.MaxGoalTokenBudget
	}
	handler := &execGoalToolHandler{
		runner:             r,
		store:              session.NewStore(filepath.Join(r.CodexHome, "sessions")),
		maxGoalTokenBudget: maxGoalTokenBudget,
	}
	return []tool.Executor{
		tool.NewExecutorFunc(tool.GoalGetToolSpec(), handler.get),
		tool.NewExecutorFunc(tool.GoalCreateToolSpec(), handler.create),
		tool.NewExecutorFunc(tool.GoalUpdateToolSpec(), handler.update),
	}
}

func (h *execGoalToolHandler) get(ctx context.Context, invocation *tool.Invocation) (*tool.Output, error) {
	threadID, _ := h.threadAndTurn()
	goal, ok := h.read(threadID)
	if !ok {
		return execGoalToolOutput(execGoalToolResponse{Goal: nil}), nil
	}
	return execGoalToolOutput(h.response(*goal, false)), nil
}

func (h *execGoalToolHandler) create(ctx context.Context, invocation *tool.Invocation) (*tool.Output, error) {
	threadID, _ := h.threadAndTurn()
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
		tokenBudget = h.maxGoalTokenBudget
	}
	if err := execValidateGoalBudget(tokenBudget, h.maxGoalTokenBudget); err != nil {
		return nil, tool.RespondToModel(err.Error())
	}
	if existing, ok := h.read(threadID); ok && !execGoalTerminal(existing.Status) {
		return nil, tool.RespondToModel("cannot create a new goal because this thread has an unfinished goal; complete the existing goal first")
	}
	now := time.Now().UTC().Unix()
	goal := &execGoal{
		ThreadID:    threadID,
		GoalID:      uuid.NewString(),
		Objective:   objective,
		TokenBudget: cloneExecInt64(tokenBudget),
		Status:      string(execGoalActive),
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	goal = execApplyGoalBudgetStatus(goal)
	if err := h.save(threadID, goal); err != nil {
		return nil, tool.RespondToModel("failed to create goal: " + err.Error())
	}
	return execGoalToolOutput(h.response(*goal, false)), nil
}

func (h *execGoalToolHandler) update(ctx context.Context, invocation *tool.Invocation) (*tool.Output, error) {
	threadID, _ := h.threadAndTurn()
	var args struct {
		Status string `json:"status"`
	}
	if err := invocation.DecodeArguments(&args); err != nil {
		return nil, tool.RespondToModel(err.Error())
	}
	status := strings.TrimSpace(args.Status)
	if status != string(execGoalComplete) && status != string(execGoalBlocked) {
		return nil, tool.RespondToModel("update_goal can only mark the existing goal complete or blocked; pause, resume, budget-limited, and usage-limited status changes are controlled by the user or system")
	}
	goal, ok := h.read(threadID)
	if !ok {
		return nil, tool.RespondToModel("cannot update goal because this thread has no goal")
	}
	goal.Status = status
	goal.UpdatedAt = time.Now().UTC().Unix()
	if err := h.save(threadID, goal); err != nil {
		return nil, tool.RespondToModel("failed to update goal: " + err.Error())
	}
	return execGoalToolOutput(h.response(*goal, status == string(execGoalComplete))), nil
}

func (h *execGoalToolHandler) read(threadID string) (*execGoal, bool) {
	if h == nil || h.store == nil || strings.TrimSpace(threadID) == "" {
		return nil, false
	}
	record, err := h.store.Read(session.ThreadID(threadID), true, false)
	if err != nil || record == nil || record.Metadata.Extra == nil {
		return nil, false
	}
	raw, ok := record.Metadata.Extra[execThreadGoalExtraKey]
	if !ok || raw == nil {
		return nil, false
	}
	data, err := json.Marshal(raw)
	if err != nil {
		return nil, false
	}
	var goal execGoal
	if err := json.Unmarshal(data, &goal); err != nil {
		return nil, false
	}
	goal.ThreadID = strings.TrimSpace(goal.ThreadID)
	if goal.ThreadID == "" {
		goal.ThreadID = threadID
	}
	goal.GoalID = strings.TrimSpace(goal.GoalID)
	if goal.GoalID == "" {
		goal.GoalID = uuid.NewString()
	}
	goal.Objective = strings.TrimSpace(goal.Objective)
	goal.Status = strings.TrimSpace(goal.Status)
	if goal.Status == "" {
		goal.Status = string(execGoalActive)
	}
	if goal.Objective == "" || !execValidGoalStatus(goal.Status) {
		return nil, false
	}
	execApplyGoalBudgetStatus(&goal)
	return &goal, true
}

func (h *execGoalToolHandler) save(threadID string, goal *execGoal) error {
	if h == nil || h.store == nil || goal == nil || strings.TrimSpace(threadID) == "" {
		return fmt.Errorf("goal handler is unavailable")
	}
	record, err := h.store.Read(session.ThreadID(threadID), true, true)
	if err != nil {
		return err
	}
	if record.Metadata.Extra == nil {
		record.Metadata.Extra = map[string]any{}
	}
	record.Metadata.Extra[execThreadGoalExtraKey] = map[string]any{
		"threadId":        strings.TrimSpace(goal.ThreadID),
		"goalId":          strings.TrimSpace(goal.GoalID),
		"objective":       strings.TrimSpace(goal.Objective),
		"status":          goal.Status,
		"tokenBudget":     cloneExecInt64(goal.TokenBudget),
		"tokensUsed":      goal.TokensUsed,
		"timeUsedSeconds": goal.TimeUsedSeconds,
		"createdAt":       goal.CreatedAt,
		"updatedAt":       goal.UpdatedAt,
	}
	return h.store.Save(record)
}

func (r *Runner) setGoalTurnContext(threadID, turnID string) {
	if r == nil || r.goalMu == nil {
		return
	}
	r.goalMu.Lock()
	r.goalThreadID = threadID
	r.goalTurnID = turnID
	r.goalMu.Unlock()
}

func (h *execGoalToolHandler) threadAndTurn() (string, string) {
	if h == nil || h.runner == nil || h.runner.goalMu == nil {
		return "", ""
	}
	h.runner.goalMu.Lock()
	defer h.runner.goalMu.Unlock()
	return h.runner.goalThreadID, h.runner.goalTurnID
}

func (h *execGoalToolHandler) response(goal execGoal, includeCompletionReport bool) execGoalToolResponse {
	response := execGoalToolResponse{Goal: &goal}
	if goal.TokenBudget != nil {
		remaining := *goal.TokenBudget - goal.TokensUsed
		if remaining < 0 {
			remaining = 0
		}
		response.RemainingTokens = &remaining
	}
	if includeCompletionReport && goal.Status == string(execGoalComplete) {
		if goal.TokenBudget != nil || goal.TimeUsedSeconds > 0 {
			report := "Goal achieved. Report final usage from this tool result's structured goal fields. If `goal.tokenBudget` is present, include token usage from `goal.tokensUsed` and `goal.tokenBudget`. If `goal.timeUsedSeconds` is greater than 0, summarize elapsed time in a concise, human-friendly form appropriate to the response language."
			response.CompletionBudgetReport = &report
		}
	}
	return response
}

type execGoalStatus string

const (
	execGoalActive        execGoalStatus = "active"
	execGoalPaused        execGoalStatus = "paused"
	execGoalBlocked       execGoalStatus = "blocked"
	execGoalUsageLimited  execGoalStatus = "usageLimited"
	execGoalBudgetLimited execGoalStatus = "budgetLimited"
	execGoalComplete      execGoalStatus = "complete"
)

func execValidGoalStatus(status string) bool {
	switch execGoalStatus(status) {
	case execGoalActive, execGoalPaused, execGoalBlocked, execGoalUsageLimited, execGoalBudgetLimited, execGoalComplete:
		return true
	default:
		return false
	}
}

func execGoalTerminal(status string) bool {
	switch execGoalStatus(status) {
	case execGoalComplete, execGoalBlocked, execGoalBudgetLimited, execGoalUsageLimited:
		return true
	default:
		return false
	}
}

func execApplyGoalBudgetStatus(goal *execGoal) *execGoal {
	if goal == nil {
		return nil
	}
	if goal.TokenBudget != nil && *goal.TokenBudget > 0 && goal.TokensUsed >= *goal.TokenBudget {
		goal.Status = string(execGoalBudgetLimited)
	}
	if goal.Status == "" {
		goal.Status = string(execGoalActive)
	}
	return goal
}

func execValidateGoalBudget(budget *int64, maxGoalTokenBudget *int64) error {
	if budget != nil && *budget <= 0 {
		return fmt.Errorf("goal budgets must be positive when provided")
	}
	if budget != nil && maxGoalTokenBudget != nil && *budget > *maxGoalTokenBudget {
		return fmt.Errorf("goal token budget %d exceeds the maximum allowed goal token budget of %d", *budget, *maxGoalTokenBudget)
	}
	return nil
}

func execGoalToolOutput(value any) *tool.Output {
	return &tool.Output{
		Success: true,
		// The Responses API requires function_call_output.output to be a
		// string or a list of output content parts. Structured tool results
		// (like the goal snapshot) must therefore be serialized to text for
		// the wire; the structured value stays in Data for internal consumers.
		Body: execGoalToolOutputBodyText(value),
		Data: map[string]any{"output": value},
	}
}

func execGoalToolOutputBodyText(value any) string {
	if text, ok := value.(string); ok {
		return text
	}
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Sprint(value)
	}
	return string(data)
}

func cloneExecInt64(value *int64) *int64 {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}
