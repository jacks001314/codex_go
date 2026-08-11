package appserver

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"codex_go/rollout"
	"codex_go/session"
	"codex_go/state"
	"codex_go/telemetry"
)

type stateGoalTurnSnapshot struct {
	GoalID       string
	StartedAtMS  int64
	ConnectionID string
}

func (r *RuntimeRouter) setStateThreadGoal(params *GoalSetParams) (*GoalSetResponse, *Goal, *session.Record, error) {
	record, _, err := r.materializedGoalThread(params.ThreadID)
	if err != nil {
		return nil, nil, nil, err
	}
	ctx := context.Background()
	existingState, err := r.services.StateRuntime.GetThreadGoal(ctx, params.ThreadID)
	if err != nil {
		return nil, nil, record, fmt.Errorf("failed to read thread goal: %w", err)
	}
	existing := apiGoalFromState(existingState)
	var persisted *state.ThreadGoal
	if params.Objective != nil {
		objective := strings.TrimSpace(*params.Objective)
		if existingState == nil {
			status := state.ThreadGoalActive
			if params.Status != nil {
				status = stateGoalStatus(*params.Status)
			}
			var budget *int64
			if params.TokenBudgetSet || params.TokenBudget != nil {
				if params.TokenBudget != nil {
					budget = cloneInt64PtrAppserver(params.TokenBudget)
				} else {
					budget = cloneInt64PtrAppserver(params.MaxGoalTokenBudget)
				}
			} else {
				budget = cloneInt64PtrAppserver(params.MaxGoalTokenBudget)
			}
			if err := validateGoalBudgetAgainstMax(budget, params.MaxGoalTokenBudget); err != nil {
				return nil, nil, record, err
			}
			persisted, err = r.services.StateRuntime.ReplaceThreadGoal(ctx, params.ThreadID, objective, status, budget)
		} else {
			update := stateGoalUpdate(params, existingState.GoalID, params.MaxGoalTokenBudget)
			if err := validateGoalBudgetAgainstMax(update.TokenBudget, params.MaxGoalTokenBudget); err != nil {
				return nil, nil, record, err
			}
			persisted, err = r.services.StateRuntime.UpdateThreadGoal(ctx, params.ThreadID, update)
		}
	} else {
		if existingState == nil {
			return nil, nil, record, jsonRPCInvalidRequest(fmt.Sprintf("cannot update goal for thread %s: no goal exists", strings.TrimSpace(params.ThreadID)))
		}
		update := stateGoalUpdate(params, existingState.GoalID, params.MaxGoalTokenBudget)
		if err := validateGoalBudgetAgainstMax(update.TokenBudget, params.MaxGoalTokenBudget); err != nil {
			return nil, nil, record, err
		}
		persisted, err = r.services.StateRuntime.UpdateThreadGoal(ctx, params.ThreadID, update)
	}
	if err != nil {
		return nil, existing, record, fmt.Errorf("failed to update thread goal: %w", err)
	}
	if persisted == nil {
		return nil, existing, record, jsonRPCInvalidRequest(fmt.Sprintf("cannot update goal for thread %s: no goal exists", strings.TrimSpace(params.ThreadID)))
	}
	goal := apiGoalFromState(persisted)
	if params.Objective != nil {
		if previewUpdated, previewErr := r.services.StateRuntime.SetThreadPreviewIfEmpty(ctx, params.ThreadID, goal.Objective); previewErr != nil {
			slog.Warn("failed to set empty thread preview from goal objective", "thread_id", params.ThreadID, "error", previewErr)
		} else if previewUpdated && strings.TrimSpace(record.Preview) == "" {
			record.Preview = goal.Objective
			if saveErr := r.runtimeSaveThreadRecord(record); saveErr != nil {
				slog.Warn("failed to mirror goal preview into compatibility thread store", "thread_id", params.ThreadID, "error", saveErr)
			}
		}
	}
	if appendErr := r.services.ThreadRouter.appendThreadGoalUpdated(*goal, "", time.Now().UTC()); appendErr != nil {
		slog.Warn("failed to persist goal update in rollout", "thread_id", params.ThreadID, "error", appendErr)
	}
	return &GoalSetResponse{Goal: *goal}, existing, record, nil
}

func (r *RuntimeRouter) getStateThreadGoal(params *GoalGetParams) (*GoalGetResponse, error) {
	if _, _, err := r.materializedGoalThread(params.ThreadID); err != nil {
		return nil, err
	}
	goal, err := r.services.StateRuntime.GetThreadGoal(context.Background(), params.ThreadID)
	if err != nil {
		return nil, fmt.Errorf("failed to read thread goal: %w", err)
	}
	return &GoalGetResponse{Goal: apiGoalFromState(goal)}, nil
}

func (r *RuntimeRouter) clearStateThreadGoal(params *GoalClearParams) (*GoalClearResponse, *Goal, *session.Record, error) {
	record, _, err := r.materializedGoalThread(params.ThreadID)
	if err != nil {
		return nil, nil, nil, err
	}
	deleted, err := r.services.StateRuntime.DeleteThreadGoal(context.Background(), params.ThreadID)
	if err != nil {
		return nil, nil, record, fmt.Errorf("failed to clear thread goal: %w", err)
	}
	return &GoalClearResponse{Cleared: deleted != nil}, apiGoalFromState(deleted), record, nil
}

func (r *RuntimeRouter) materializedGoalThread(threadID string) (*session.Record, string, error) {
	threadID = strings.TrimSpace(threadID)
	if _, ok := r.ephemeralThreadRecord(session.ThreadID(threadID), false); ok {
		return nil, "", jsonRPCInvalidRequest(fmt.Sprintf("ephemeral thread does not support goals: %s", threadID))
	}
	record, err := r.threadRecord(session.ThreadID(threadID), true, false)
	if err != nil {
		return nil, "", err
	}
	path := r.services.ThreadRouter.threadRolloutPath(record)
	if strings.TrimSpace(path) == "" {
		return nil, "", jsonRPCInvalidRequest(fmt.Sprintf("thread not found: %s", threadID))
	}
	if err := r.services.StateRuntime.ReconcileRollout(context.Background(), path, record.Archived); err != nil {
		return nil, "", fmt.Errorf("failed to reconcile thread rollout: %w", err)
	}
	return record, path, nil
}

func stateGoalUpdate(params *GoalSetParams, expectedGoalID string, maxGoalTokenBudget *int64) state.GoalUpdate {
	update := state.GoalUpdate{TokenBudgetSet: params.TokenBudgetSet || params.TokenBudget != nil}
	if params.Objective != nil {
		objective := strings.TrimSpace(*params.Objective)
		update.Objective = &objective
	}
	if params.Status != nil {
		status := stateGoalStatus(*params.Status)
		update.Status = &status
	}
	if update.TokenBudgetSet {
		if params.TokenBudget != nil {
			update.TokenBudget = cloneInt64PtrAppserver(params.TokenBudget)
		} else {
			update.TokenBudget = cloneInt64PtrAppserver(maxGoalTokenBudget)
		}
	}
	if strings.TrimSpace(expectedGoalID) != "" {
		expectedGoalID = strings.TrimSpace(expectedGoalID)
		update.ExpectedGoalID = &expectedGoalID
	}
	return update
}

func validateGoalBudgetAgainstMax(budget *int64, maxGoalTokenBudget *int64) error {
	if budget != nil && maxGoalTokenBudget != nil && *budget > *maxGoalTokenBudget {
		return jsonRPCInvalidRequest(fmt.Sprintf("goal token budget %d exceeds the maximum allowed goal token budget of %d", *budget, *maxGoalTokenBudget))
	}
	return nil
}

func apiGoalFromState(goal *state.ThreadGoal) *Goal {
	if goal == nil {
		return nil
	}
	return &Goal{
		ThreadID: goal.ThreadID, GoalID: goal.GoalID, Objective: goal.Objective,
		Status: apiGoalStatus(goal.Status), TokenBudget: cloneInt64PtrAppserver(goal.TokenBudget),
		TokensUsed: goal.TokensUsed, TimeUsedSeconds: goal.TimeUsedSeconds,
		CreatedAt: goal.CreatedAt.Unix(), UpdatedAt: goal.UpdatedAt.Unix(),
	}
}

func stateGoalStatus(status GoalStatus) state.ThreadGoalStatus {
	switch status {
	case GoalPaused:
		return state.ThreadGoalPaused
	case GoalBlocked:
		return state.ThreadGoalBlocked
	case GoalUsageLimited:
		return state.ThreadGoalUsageLimited
	case GoalBudgetLimited:
		return state.ThreadGoalBudgetLimited
	case GoalComplete:
		return state.ThreadGoalComplete
	default:
		return state.ThreadGoalActive
	}
}

func apiGoalStatus(status state.ThreadGoalStatus) GoalStatus {
	switch status {
	case state.ThreadGoalPaused:
		return GoalPaused
	case state.ThreadGoalBlocked:
		return GoalBlocked
	case state.ThreadGoalUsageLimited:
		return GoalUsageLimited
	case state.ThreadGoalBudgetLimited:
		return GoalBudgetLimited
	case state.ThreadGoalComplete:
		return GoalComplete
	default:
		return GoalActive
	}
}

func (r *Router) appendThreadGoalUpdated(goal Goal, turnID string, now time.Time) error {
	if r == nil || r.store == nil {
		return nil
	}
	path, err := r.findThreadRolloutPath(session.ThreadID(goal.ThreadID), false)
	if err != nil {
		return err
	}
	recorder, err := rollout.Resume(path)
	if err != nil {
		return err
	}
	r.configureThreadHistoryRecorder(recorder, session.ThreadID(goal.ThreadID))
	defer recorder.Close()
	return recorder.AppendThreadGoalUpdated(rollout.ThreadGoal{
		ThreadID: goal.ThreadID, Objective: goal.Objective, Status: string(goal.Status),
		TokenBudget: cloneInt64PtrAppserver(goal.TokenBudget), TokensUsed: goal.TokensUsed,
		TimeUsedSeconds: goal.TimeUsedSeconds, CreatedAt: goal.CreatedAt, UpdatedAt: goal.UpdatedAt,
	}, turnID, now)
}

func (r *RuntimeRouter) beginStateThreadGoalTurn(threadID, turnID string, startedAtMS int64, planMode bool, connectionID string) {
	if r == nil || r.services.StateRuntime == nil || strings.TrimSpace(threadID) == "" || strings.TrimSpace(turnID) == "" {
		return
	}
	ctx := context.Background()
	if err := r.services.StateRuntime.ClearThreadGoalContinuationDeferral(ctx, threadID); err != nil {
		slog.Warn("failed to clear deferred goal continuation", "thread_id", threadID, "error", err)
	}
	if planMode {
		return
	}
	goal, err := r.services.StateRuntime.GetThreadGoal(ctx, threadID)
	if err != nil {
		slog.Warn("failed to read thread goal at turn start", "thread_id", threadID, "error", err)
		return
	}
	if goal == nil || (goal.Status != state.ThreadGoalActive && goal.Status != state.ThreadGoalBudgetLimited) {
		return
	}
	if startedAtMS <= 0 {
		startedAtMS = time.Now().UTC().UnixMilli()
	}
	r.goalAccountingMu.Lock()
	if r.goalAccountingTurns == nil {
		r.goalAccountingTurns = map[string]stateGoalTurnSnapshot{}
	}
	r.goalAccountingTurns[stateGoalTurnKey(threadID, turnID)] = stateGoalTurnSnapshot{
		GoalID: goal.GoalID, StartedAtMS: startedAtMS, ConnectionID: strings.TrimSpace(connectionID),
	}
	r.goalAccountingMu.Unlock()
}

func (r *RuntimeRouter) finishStateThreadGoalTurn(threadID, turnID string, completedAt time.Time, tokenDelta int64, turnErr CodexErrorInfo) {
	if r == nil || r.services.StateRuntime == nil {
		return
	}
	key := stateGoalTurnKey(threadID, turnID)
	r.goalAccountingMu.Lock()
	snapshot, ok := r.goalAccountingTurns[key]
	delete(r.goalAccountingTurns, key)
	r.goalAccountingMu.Unlock()
	if !ok || strings.TrimSpace(snapshot.GoalID) == "" {
		return
	}
	if completedAt.IsZero() {
		completedAt = time.Now().UTC()
	}
	timeDeltaSeconds := (completedAt.UTC().UnixMilli() - snapshot.StartedAtMS) / 1000
	if timeDeltaSeconds < 0 {
		timeDeltaSeconds = 0
	}
	expectedGoalID := snapshot.GoalID
	outcome, err := r.services.StateRuntime.AccountThreadGoalUsage(
		context.Background(), threadID, timeDeltaSeconds, tokenDelta,
		state.GoalAccountingActiveOnly, &expectedGoalID,
	)
	if err != nil {
		slog.Warn("failed to account thread goal usage", "thread_id", threadID, "turn_id", turnID, "error", err)
		return
	}
	if outcome != nil && outcome.Updated && outcome.Goal != nil {
		r.emitStateThreadGoalUpdate(outcome.Goal, turnID, snapshot.ConnectionID, telemetry.GoalEventKindUsageAccounted)
	}
	if turnErr == nil {
		return
	}
	current, err := r.services.StateRuntime.GetThreadGoal(context.Background(), threadID)
	if err != nil || current == nil || current.GoalID != expectedGoalID {
		return
	}
	status := state.ThreadGoalBlocked
	if codexErrorIsUsageLimited(turnErr) {
		status = state.ThreadGoalUsageLimited
	}
	updated, err := r.services.StateRuntime.UpdateThreadGoal(context.Background(), threadID, state.GoalUpdate{
		Status: &status, ExpectedGoalID: &expectedGoalID,
	})
	if err != nil {
		slog.Warn("failed to stop thread goal after turn error", "thread_id", threadID, "turn_id", turnID, "error", err)
		return
	}
	if updated != nil && updated.Status != current.Status {
		r.emitStateThreadGoalUpdate(updated, turnID, snapshot.ConnectionID, telemetry.GoalEventKindStatusChanged)
	}
}

func (r *RuntimeRouter) emitStateThreadGoalUpdate(goal *state.ThreadGoal, turnID, connectionID, analyticsKind string) {
	apiGoal := apiGoalFromState(goal)
	if apiGoal == nil {
		return
	}
	if r.services.ThreadRouter != nil {
		if err := r.services.ThreadRouter.appendThreadGoalUpdated(*apiGoal, turnID, time.Now().UTC()); err != nil {
			slog.Warn("failed to persist turn goal update in rollout", "thread_id", goal.ThreadID, "turn_id", turnID, "error", err)
		}
	}
	turnIDCopy := strings.TrimSpace(turnID)
	r.notify(NotificationThreadGoalUpdated, &GoalUpdatedNotification{ThreadID: apiGoal.ThreadID, TurnID: &turnIDCopy, Goal: *apiGoal})
	r.emitGoalAnalyticsEvent(context.Background(), connectionID, nil, apiGoal, analyticsKind, &turnIDCopy)
}

func stateGoalTurnKey(threadID, turnID string) string {
	return strings.TrimSpace(threadID) + "\x00" + strings.TrimSpace(turnID)
}

func codexErrorIsUsageLimited(value CodexErrorInfo) bool {
	switch typed := value.(type) {
	case string:
		return strings.EqualFold(strings.TrimSpace(typed), "usageLimitExceeded")
	case map[string]any:
		_, ok := typed["usageLimitExceeded"]
		return ok
	default:
		return false
	}
}

func (r *Router) inheritThreadGoalSnapshot(sourceThreadID, targetThreadID session.ThreadID) (bool, error) {
	if r == nil || r.state == nil {
		return false, nil
	}
	goal, err := r.state.GetThreadGoal(context.Background(), string(sourceThreadID))
	if err != nil || goal == nil {
		return false, err
	}
	objective := strings.TrimSpace(goal.Objective)
	if objective == "" || len([]rune(objective)) > 4000 {
		slog.Warn("skipping invalid inherited thread goal", "source_thread_id", sourceThreadID)
		return false, nil
	}
	goal.ThreadID = string(targetThreadID)
	if err := r.state.ReplaceThreadGoalSnapshot(context.Background(), goal); err != nil {
		return false, err
	}
	return true, nil
}
