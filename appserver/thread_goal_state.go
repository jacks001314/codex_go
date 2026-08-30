package appserver

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"codex_go/model"
	"codex_go/prompt"
	"codex_go/rollout"
	"codex_go/session"
	"codex_go/state"
	"codex_go/telemetry"
	"codex_go/turn"
)

type stateGoalTurnSnapshot struct {
	GoalID             string
	StartedAtMS        int64
	LastAccountedAtMS  int64
	LastAccountedUsage model.AgentUsage
	ConnectionID       string
	FinishMode         state.GoalAccountingMode
	// Execution-failure tracking for the active goal (Rust #41454): a turn records a
	// failed_execution when a default-namespace `exec` tool call ran its handler
	// and failed, and successful_tool when any tool completed successfully. A
	// goal is blocked after three consecutive turns of handler-executed exec
	// failures with no intervening successful tool.
	SuccessfulTool  bool
	FailedExecution bool
}

// goalExecutionFailureState carries the consecutive-execution-failure streak for
// a thread's active goal (Rust #41454). The counter accumulates across turns for
// the same goal and resets when a tool succeeds, when the active goal changes,
// or when the goal is cleared.
type goalExecutionFailureState struct {
	GoalID string
	Turns  int
}

func goalTokenDeltaForUsage(usage model.AgentUsage) int64 {
	input := usage.InputTokens - usage.CachedInputTokens
	if input < 0 {
		input = 0
	}
	output := usage.OutputTokens
	if output < 0 {
		output = 0
	}
	return input + output
}

func goalTokenDelta(last, current model.AgentUsage) int64 {
	delta := model.AgentUsage{
		InputTokens:           current.InputTokens - last.InputTokens,
		CachedInputTokens:     current.CachedInputTokens - last.CachedInputTokens,
		CacheWriteInputTokens: current.CacheWriteInputTokens - last.CacheWriteInputTokens,
		OutputTokens:          current.OutputTokens - last.OutputTokens,
		ReasoningOutputTokens: current.ReasoningOutputTokens - last.ReasoningOutputTokens,
		TotalTokens:           current.TotalTokens - last.TotalTokens,
	}
	if delta.InputTokens < 0 {
		delta.InputTokens = 0
	}
	if delta.CachedInputTokens < 0 {
		delta.CachedInputTokens = 0
	}
	if delta.CacheWriteInputTokens < 0 {
		delta.CacheWriteInputTokens = 0
	}
	if delta.OutputTokens < 0 {
		delta.OutputTokens = 0
	}
	if delta.ReasoningOutputTokens < 0 {
		delta.ReasoningOutputTokens = 0
	}
	if delta.TotalTokens < 0 {
		delta.TotalTokens = 0
	}
	return goalTokenDeltaForUsage(delta)
}

func (r *RuntimeRouter) setStateThreadGoal(params *GoalSetParams) (*GoalSetResponse, *Goal, *session.Record, error) {
	record, _, err := r.materializedGoalThread(params.ThreadID, true)
	if err != nil {
		return nil, nil, nil, err
	}
	r.goalStateMu.Lock()
	locked := true
	defer func() {
		if locked {
			r.goalStateMu.Unlock()
		}
	}()
	r.prepareExternalGoalMutation(params.ThreadID)
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
	locked = false
	r.goalStateMu.Unlock()
	r.applyGoalActiveRuntimeEffects(goal.ThreadID, *goal)
	return &GoalSetResponse{Goal: *goal}, existing, record, nil
}

func (r *RuntimeRouter) getStateThreadGoal(params *GoalGetParams) (*GoalGetResponse, error) {
	if _, _, err := r.materializedGoalThread(params.ThreadID, false); err != nil {
		return nil, err
	}
	goal, err := r.services.StateRuntime.GetThreadGoal(context.Background(), params.ThreadID)
	if err != nil {
		return nil, fmt.Errorf("failed to read thread goal: %w", err)
	}
	return &GoalGetResponse{Goal: apiGoalFromState(goal)}, nil
}

func (r *RuntimeRouter) clearStateThreadGoal(params *GoalClearParams) (*GoalClearResponse, *Goal, *session.Record, error) {
	record, _, err := r.materializedGoalThread(params.ThreadID, true)
	if err != nil {
		return nil, nil, nil, err
	}
	r.goalStateMu.Lock()
	locked := true
	defer func() {
		if locked {
			r.goalStateMu.Unlock()
		}
	}()
	r.prepareExternalGoalMutation(params.ThreadID)
	deleted, err := r.services.StateRuntime.DeleteThreadGoal(context.Background(), params.ThreadID)
	if err != nil {
		return nil, nil, record, fmt.Errorf("failed to clear thread goal: %w", err)
	}
	locked = false
	r.goalStateMu.Unlock()
	if deleted != nil {
		r.clearActiveGoalStateForThread(params.ThreadID)
	}
	return &GoalClearResponse{Cleared: deleted != nil}, apiGoalFromState(deleted), record, nil
}

// materializedGoalThread resolves a persisted thread for a goal operation and
// reconciles its rollout row into the state runtime. When materialize is true
// (set/clear) a missing rollout file is created first, mirroring Rust's
// reconcile_thread_goal_rollout -> reconcile_rollout which materializes
// "goal-first" threads on demand. Reads (materialize=false) skip the reconcile
// when the rollout file does not exist yet, matching Rust's thread_goal_get
// which reads the goal database without reconciling.
func (r *RuntimeRouter) materializedGoalThread(threadID string, materialize bool) (*session.Record, string, error) {
	threadID = strings.TrimSpace(threadID)
	if _, ok := r.ephemeralThreadRecord(session.ThreadID(threadID), false); ok {
		return nil, "", jsonRPCInvalidRequest(fmt.Sprintf("ephemeral thread does not support goals: %s", threadID))
	}
	record, err := r.threadRecord(session.ThreadID(threadID), true, false)
	if err != nil {
		if errors.Is(err, session.ErrThreadNotFound) || strings.Contains(err.Error(), "thread not found") {
			return nil, "", jsonRPCInvalidRequest(fmt.Sprintf("thread not found: %s", threadID))
		}
		return nil, "", err
	}
	path := r.services.ThreadRouter.threadRolloutPath(record)
	if strings.TrimSpace(path) == "" {
		return nil, "", jsonRPCInvalidRequest(fmt.Sprintf("thread not found: %s", threadID))
	}
	if _, err := os.Stat(path); os.IsNotExist(err) {
		if !materialize {
			// Fresh thread with no rollout yet: the goal database is authoritative.
			return record, path, nil
		}
		now := record.CreatedAt
		if now.IsZero() {
			now = r.services.ThreadRouter.now().UTC()
		}
		if err := r.services.ThreadRouter.createThreadRollout(record, now); err != nil {
			return nil, "", fmt.Errorf("failed to materialize thread rollout: %w", err)
		}
		path = r.services.ThreadRouter.threadRolloutPath(record)
		if strings.TrimSpace(path) == "" {
			return nil, "", jsonRPCInvalidRequest(fmt.Sprintf("thread not found: %s", threadID))
		}
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

func (r *RuntimeRouter) markStateThreadGoalTurnActiveNow(threadID, turnID, goalID string) {
	if r == nil || r.services.StateRuntime == nil {
		return
	}
	threadID = strings.TrimSpace(threadID)
	turnID = strings.TrimSpace(turnID)
	goalID = strings.TrimSpace(goalID)
	if threadID == "" || turnID == "" || goalID == "" {
		return
	}
	nowMS := time.Now().UTC().UnixMilli()
	r.goalAccountingMu.Lock()
	if r.goalAccountingTurns == nil {
		r.goalAccountingTurns = map[string]stateGoalTurnSnapshot{}
	}
	if r.goalTurnUsage == nil {
		r.goalTurnUsage = map[string]model.AgentUsage{}
	}
	if r.descendantTokenUsage == nil {
		r.descendantTokenUsage = map[string]int64{}
	}
	if r.lastAccountedDescendant == nil {
		r.lastAccountedDescendant = map[string]int64{}
	}
	currentUsage := r.goalTurnUsage[stateGoalTurnKey(threadID, turnID)]
	r.goalAccountingTurns[stateGoalTurnKey(threadID, turnID)] = stateGoalTurnSnapshot{
		GoalID:             goalID,
		StartedAtMS:        nowMS,
		LastAccountedAtMS:  nowMS,
		LastAccountedUsage: currentUsage,
		FinishMode:         state.GoalAccountingActiveOnly,
	}
	// Rust #41183: anchoring a goal re-baselines descendant token accounting so
	// descendant usage does not carry across to a replacement goal.
	r.lastAccountedDescendant[strings.TrimSpace(threadID)] = r.descendantTokenUsage[strings.TrimSpace(threadID)]
	r.goalAccountingMu.Unlock()
	r.clearGoalIdleActive()
}

func (r *RuntimeRouter) ensureStateThreadGoalTurnActive(threadID, turnID, goalID string) {
	if r == nil || r.services.StateRuntime == nil {
		return
	}
	threadID = strings.TrimSpace(threadID)
	turnID = strings.TrimSpace(turnID)
	goalID = strings.TrimSpace(goalID)
	if threadID == "" || turnID == "" || goalID == "" {
		return
	}
	key := stateGoalTurnKey(threadID, turnID)
	nowMS := time.Now().UTC().UnixMilli()
	r.goalAccountingMu.Lock()
	if r.goalAccountingTurns == nil {
		r.goalAccountingTurns = map[string]stateGoalTurnSnapshot{}
	}
	if r.descendantTokenUsage == nil {
		r.descendantTokenUsage = map[string]int64{}
	}
	if r.lastAccountedDescendant == nil {
		r.lastAccountedDescendant = map[string]int64{}
	}
	if existing, exists := r.goalAccountingTurns[key]; exists {
		// Rust #41454 mark_current_turn_goal_active: when the active goal changes
		// mid-turn, reset the per-turn execution-failure flags so the failure
		// streak does not carry across to the replacement goal.
		if existing.GoalID != goalID {
			existing.SuccessfulTool = false
			existing.FailedExecution = false
			r.goalAccountingTurns[key] = existing
			// A replacement goal restarts the per-thread consecutive counter.
			delete(r.execFailureTurns, strings.TrimSpace(threadID))
			// A replacement goal re-baselines descendant token accounting.
			r.lastAccountedDescendant[strings.TrimSpace(threadID)] = r.descendantTokenUsage[strings.TrimSpace(threadID)]
		}
		r.goalAccountingMu.Unlock()
		return
	}
	if r.goalTurnUsage == nil {
		r.goalTurnUsage = map[string]model.AgentUsage{}
	}
	currentUsage := r.goalTurnUsage[key]
	r.goalAccountingTurns[key] = stateGoalTurnSnapshot{
		GoalID:             goalID,
		StartedAtMS:        nowMS,
		LastAccountedAtMS:  nowMS,
		LastAccountedUsage: currentUsage,
		FinishMode:         state.GoalAccountingActiveOnly,
	}
	r.goalAccountingMu.Unlock()
	r.clearGoalIdleActive()
}

func (r *RuntimeRouter) clearActiveGoalStateForThread(threadID string) {
	if r == nil {
		return
	}
	if active := r.threads.ActiveTurn(threadID); active != nil && strings.TrimSpace(active.TurnID) != "" {
		r.clearStateThreadGoalTurnSnapshot(threadID, strings.TrimSpace(active.TurnID))
	}
	r.clearGoalIdleActive()
	// A cleared goal ends the active-goal execution-failure streak (Rust
	// #41454 clear_current_turn_goal / clear_active_goal reset the live counter).
	r.resetGoalExecutionFailure(threadID)
}

// advanceGoalExecutionFailure evaluates the per-thread consecutive-execution-
// failure streak for the just-finished turn (Rust #41454 execution_failure_goal).
// A turn with a successful tool resets the streak; only a turn with a
// handler-executed `exec` failure advances it. The streak is keyed by thread so
// it does not leak across threads, and is re-anchored to the active goal when
// the goal changes. It returns the active goal ID when the streak reaches three.
func (r *RuntimeRouter) advanceGoalExecutionFailure(threadID, goalID string, snapshot stateGoalTurnSnapshot) string {
	if r == nil || strings.TrimSpace(threadID) == "" || strings.TrimSpace(goalID) == "" {
		return ""
	}
	r.goalAccountingMu.Lock()
	defer r.goalAccountingMu.Unlock()
	if snapshot.SuccessfulTool {
		delete(r.execFailureTurns, threadID)
		return ""
	}
	if !snapshot.FailedExecution {
		return ""
	}
	st := r.execFailureTurns[threadID]
	if st.GoalID != goalID {
		st.GoalID = goalID
		st.Turns = 0
	}
	st.Turns++
	r.execFailureTurns[threadID] = st
	if st.Turns >= 3 {
		return goalID
	}
	return ""
}

func (r *RuntimeRouter) resetGoalExecutionFailure(threadID string) {
	if r == nil {
		return
	}
	r.goalAccountingMu.Lock()
	defer r.goalAccountingMu.Unlock()
	delete(r.execFailureTurns, strings.TrimSpace(threadID))
}

func (r *RuntimeRouter) recordGoalTokenUsage(threadID, turnID string, usage model.AgentUsage) {
	if r == nil || strings.TrimSpace(threadID) == "" || strings.TrimSpace(turnID) == "" {
		return
	}
	key := stateGoalTurnKey(threadID, turnID)
	r.goalAccountingMu.Lock()
	if r.goalTurnUsage == nil {
		r.goalTurnUsage = map[string]model.AgentUsage{}
	}
	r.goalTurnUsage[key] = usage
	r.goalAccountingMu.Unlock()
}

// recordGoalTokenUsageWithDescendants records a turn's own token usage (for the
// thread's own goal, if any) and, when the thread is a spawned descendant
// (subagent), rolls the same usage into its root ancestor goal's descendant
// budget (Rust #41183). The descendant rollup lets subagent token spend count
// against the root goal.
func (r *RuntimeRouter) recordGoalTokenUsageWithDescendants(threadID, turnID string, usage model.AgentUsage) {
	if r == nil {
		return
	}
	r.recordGoalTokenUsage(threadID, turnID, usage)
	rootID := r.rootGoalThreadID(threadID)
	if rootID != "" && rootID != strings.TrimSpace(threadID) {
		r.recordDescendantGoalTokenUsage(rootID, usage)
	}
}

// rootGoalThreadID walks the thread spawn lineage to the root thread that owns
// the goal a subagent's usage should be attributed to. It returns the thread
// itself when it has no parent (a root thread).
func (r *RuntimeRouter) rootGoalThreadID(threadID string) string {
	if r == nil || r.threads == nil {
		return strings.TrimSpace(threadID)
	}
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return ""
	}
	rootID := threadID
	record, err := r.threadRecord(session.ThreadID(threadID), false, false)
	for err == nil && record != nil && strings.TrimSpace(string(record.ParentThreadID)) != "" {
		rootID = strings.TrimSpace(string(record.ParentThreadID))
		record, err = r.threadRecord(record.ParentThreadID, false, false)
	}
	return rootID
}

// recordDescendantGoalTokenUsage mirrors Rust goal-accounting #41183: token
// usage from a spawned descendant (a subagent or nested agent) is rolled into
// the root goal's usage so it contributes to that goal's token budget. The
// delta since the last recorded descendant usage is accumulated per thread;
// the accounting-side (active/idle) consumes it via descendantTokenDelta.
func (r *RuntimeRouter) recordDescendantGoalTokenUsage(threadID string, usage model.AgentUsage) {
	if r == nil || strings.TrimSpace(threadID) == "" {
		return
	}
	delta := goalTokenDeltaForUsage(usage)
	if delta <= 0 {
		return
	}
	threadID = strings.TrimSpace(threadID)
	r.goalAccountingMu.Lock()
	if r.descendantTokenUsage == nil {
		r.descendantTokenUsage = map[string]int64{}
	}
	r.descendantTokenUsage[threadID] += delta
	r.goalAccountingMu.Unlock()
}

// descendantTokenDelta returns the descendant token usage accumulated for a
// thread since the last accounted baseline, and records it as accounted. The
// caller must hold goalProgressMu (via accountStateThreadGoalProgress or
// accountIdleGoalProgress) to avoid double-counting.
func (r *RuntimeRouter) descendantTokenDelta(threadID string) int64 {
	if r == nil {
		return 0
	}
	threadID = strings.TrimSpace(threadID)
	r.goalAccountingMu.Lock()
	defer r.goalAccountingMu.Unlock()
	current := r.descendantTokenUsage[threadID]
	baseline := r.lastAccountedDescendant[threadID]
	delta := current - baseline
	if delta < 0 {
		delta = 0
	}
	r.lastAccountedDescendant[threadID] = current
	return delta
}

// resetDescendantTokenBaseline re-anchors the descendant baseline for a thread
// so a goal change does not carry descendant usage across to the replacement
// goal (Rust #41183).
func (r *RuntimeRouter) resetDescendantTokenBaseline(threadID string) {
	if r == nil {
		return
	}
	threadID = strings.TrimSpace(threadID)
	r.goalAccountingMu.Lock()
	defer r.goalAccountingMu.Unlock()
	if r.lastAccountedDescendant == nil {
		r.lastAccountedDescendant = map[string]int64{}
	}
	r.lastAccountedDescendant[threadID] = r.descendantTokenUsage[threadID]
}

func (r *RuntimeRouter) markGoalIdleActive(goalID string) {
	if r == nil {
		return
	}
	goalID = strings.TrimSpace(goalID)
	if goalID == "" {
		return
	}
	now := time.Now().UTC()
	r.goalIdleMu.Lock()
	if r.goalIdleGoalID != goalID || r.goalIdleLastAccounted.IsZero() {
		r.goalIdleLastAccounted = now
	}
	r.goalIdleGoalID = goalID
	r.goalIdleMu.Unlock()
}

func (r *RuntimeRouter) clearGoalIdleActive() {
	if r == nil {
		return
	}
	r.goalIdleMu.Lock()
	r.goalIdleGoalID = ""
	r.goalIdleLastAccounted = time.Now().UTC()
	r.goalIdleMu.Unlock()
}

func (r *RuntimeRouter) accountIdleGoalProgress(threadID string) *state.GoalAccountingOutcome {
	if r == nil || r.services.StateRuntime == nil {
		return nil
	}
	r.goalProgressMu.Lock()
	defer r.goalProgressMu.Unlock()
	now := time.Now().UTC()
	r.goalIdleMu.Lock()
	goalID := r.goalIdleGoalID
	last := r.goalIdleLastAccounted
	if goalID == "" || last.IsZero() {
		r.goalIdleMu.Unlock()
		return nil
	}
	r.goalIdleMu.Unlock()
	timeDelta := now.UnixMilli() - last.UnixMilli()
	if timeDelta < 0 {
		timeDelta = 0
	}
	seconds := timeDelta / 1000
	descendantDelta := r.descendantTokenDelta(threadID)
	outcome, err := r.services.StateRuntime.AccountThreadGoalUsage(
		context.Background(), threadID, seconds, descendantDelta, state.GoalAccountingActiveOnly, &goalID,
	)
	if err != nil {
		slog.Warn("failed to account idle goal progress", "thread_id", threadID, "error", err)
		return nil
	}
	if outcome != nil && outcome.Updated && outcome.Goal != nil {
		r.goalIdleMu.Lock()
		if seconds > 0 {
			r.goalIdleLastAccounted = last.Add(time.Duration(seconds) * time.Second)
		}
		r.goalIdleMu.Unlock()
		r.emitStateThreadGoalUpdate(outcome.Goal, "", "", telemetry.GoalEventKindUsageAccounted)
		if outcome.Goal.Status != state.ThreadGoalActive {
			r.clearGoalIdleActive()
		}
	}
	return outcome
}

func (r *RuntimeRouter) prepareExternalGoalMutation(threadID string) {
	if r == nil || r.services.StateRuntime == nil {
		return
	}
	if active := r.threads.ActiveTurn(threadID); active != nil && strings.TrimSpace(active.TurnID) != "" {
		r.accountStateThreadGoalProgress(threadID, strings.TrimSpace(active.TurnID), time.Now().UTC(), state.GoalAccountingActiveOnly)
		return
	}
	r.accountIdleGoalProgress(threadID)
}

func (r *RuntimeRouter) accountStateThreadGoalProgress(threadID, turnID string, now time.Time, mode state.GoalAccountingMode) *state.GoalAccountingOutcome {
	if r == nil || r.services.StateRuntime == nil {
		return nil
	}
	r.goalProgressMu.Lock()
	defer r.goalProgressMu.Unlock()
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if mode == "" {
		mode = state.GoalAccountingActiveOnly
	}
	key := stateGoalTurnKey(threadID, turnID)
	r.goalAccountingMu.Lock()
	snapshot, ok := r.goalAccountingTurns[key]
	if !ok || strings.TrimSpace(snapshot.GoalID) == "" {
		r.goalAccountingMu.Unlock()
		return nil
	}
	if r.goalTurnUsage == nil {
		r.goalTurnUsage = map[string]model.AgentUsage{}
	}
	currentUsage := r.goalTurnUsage[key]
	tokenDelta := goalTokenDelta(snapshot.LastAccountedUsage, currentUsage)
	startedAtMS := snapshot.LastAccountedAtMS
	if startedAtMS == 0 {
		startedAtMS = snapshot.StartedAtMS
	}
	timeDeltaSeconds := (now.UTC().UnixMilli() - startedAtMS) / 1000
	if timeDeltaSeconds < 0 {
		timeDeltaSeconds = 0
	}
	expectedGoalID := snapshot.GoalID
	connectionID := snapshot.ConnectionID
	r.goalAccountingMu.Unlock()
	descendantDelta := r.descendantTokenDelta(threadID)
	if descendantDelta > 0 {
		tokenDelta += descendantDelta
	}

	outcome, err := r.services.StateRuntime.AccountThreadGoalUsage(
		context.Background(), threadID, timeDeltaSeconds, tokenDelta, mode, &expectedGoalID,
	)
	if err != nil {
		slog.Warn("failed to account active goal progress", "thread_id", threadID, "turn_id", turnID, "error", err)
		return nil
	}
	if outcome != nil && outcome.Updated && outcome.Goal != nil {
		r.goalAccountingMu.Lock()
		if updated, exists := r.goalAccountingTurns[key]; exists && updated.GoalID == expectedGoalID {
			updated.LastAccountedAtMS = now.UTC().UnixMilli()
			updated.LastAccountedUsage = currentUsage
			r.goalAccountingTurns[key] = updated
		}
		r.goalAccountingMu.Unlock()
		r.emitStateThreadGoalUpdate(outcome.Goal, turnID, connectionID, telemetry.GoalEventKindUsageAccounted)
		if outcome.Goal.Status == state.ThreadGoalBudgetLimited {
			r.enqueueGoalBudgetLimitSteering(threadID, turnID, outcome.Goal)
		}
	}
	return outcome
}

func (r *RuntimeRouter) enqueueGoalBudgetLimitSteering(threadID, turnID string, goal *state.ThreadGoal) {
	if r == nil || goal == nil {
		return
	}
	item := modelInputTextMessage("developer", prompt.BudgetLimit(goalContinuationPrompt(goal)))
	if err := r.requireSteerMailbox().Enqueue(&turn.SteerEnqueueParams{
		ThreadID:   threadID,
		TurnID:     turnID,
		InputItems: []any{item},
	}); err != nil {
		slog.Warn("failed to enqueue goal budget limit steering", "thread_id", threadID, "turn_id", turnID, "error", err)
	}
}

func (r *RuntimeRouter) setStateThreadGoalTurnFinishMode(threadID, turnID string, mode state.GoalAccountingMode) bool {
	if r == nil {
		return false
	}
	key := stateGoalTurnKey(threadID, turnID)
	r.goalAccountingMu.Lock()
	defer r.goalAccountingMu.Unlock()
	snapshot, ok := r.goalAccountingTurns[key]
	if !ok {
		return false
	}
	snapshot.FinishMode = mode
	r.goalAccountingTurns[key] = snapshot
	return true
}

func (r *RuntimeRouter) clearStateThreadGoalTurnSnapshot(threadID, turnID string) {
	if r == nil {
		return
	}
	r.goalAccountingMu.Lock()
	delete(r.goalAccountingTurns, stateGoalTurnKey(threadID, turnID))
	r.goalAccountingMu.Unlock()
}

func (r *RuntimeRouter) stateThreadGoalTurnExpectedID(threadID, turnID string) *string {
	if r == nil {
		return nil
	}
	r.goalAccountingMu.Lock()
	defer r.goalAccountingMu.Unlock()
	snapshot, ok := r.goalAccountingTurns[stateGoalTurnKey(threadID, turnID)]
	if !ok || strings.TrimSpace(snapshot.GoalID) == "" {
		return nil
	}
	goalID := snapshot.GoalID
	return &goalID
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
	if r.goalTurnUsage == nil {
		r.goalTurnUsage = map[string]model.AgentUsage{}
	}
	currentUsage := r.goalTurnUsage[stateGoalTurnKey(threadID, turnID)]
	r.goalAccountingTurns[stateGoalTurnKey(threadID, turnID)] = stateGoalTurnSnapshot{
		GoalID:             goal.GoalID,
		StartedAtMS:        startedAtMS,
		LastAccountedAtMS:  startedAtMS,
		LastAccountedUsage: currentUsage,
		ConnectionID:       strings.TrimSpace(connectionID),
		FinishMode:         state.GoalAccountingActiveOnly,
	}
	r.goalAccountingMu.Unlock()
	r.clearGoalIdleActive()
}

func (r *RuntimeRouter) finishStateThreadGoalTurn(threadID, turnID string, completedAt time.Time, tokenDelta int64, turnErr CodexErrorInfo) {
	if r == nil || r.services.StateRuntime == nil {
		return
	}
	r.goalProgressMu.Lock()
	defer r.goalProgressMu.Unlock()
	key := stateGoalTurnKey(threadID, turnID)
	r.goalAccountingMu.Lock()
	snapshot, ok := r.goalAccountingTurns[key]
	if r.goalTurnUsage == nil {
		r.goalTurnUsage = map[string]model.AgentUsage{}
	}
	if currentUsage, hasUsage := r.goalTurnUsage[key]; ok && hasUsage {
		tokenDelta = goalTokenDelta(snapshot.LastAccountedUsage, currentUsage)
		delete(r.goalTurnUsage, key)
	}
	delete(r.goalAccountingTurns, key)
	r.goalAccountingMu.Unlock()
	if !ok || strings.TrimSpace(snapshot.GoalID) == "" {
		return
	}
	if completedAt.IsZero() {
		completedAt = time.Now().UTC()
	}
	startedAtMS := snapshot.LastAccountedAtMS
	if startedAtMS == 0 {
		startedAtMS = snapshot.StartedAtMS
	}
	timeDeltaSeconds := (completedAt.UTC().UnixMilli() - startedAtMS) / 1000
	if timeDeltaSeconds < 0 {
		timeDeltaSeconds = 0
	}
	mode := snapshot.FinishMode
	if mode == "" {
		mode = state.GoalAccountingActiveOnly
	}
	finishMode := mode
	expectedGoalID := snapshot.GoalID
	outcome, err := r.services.StateRuntime.AccountThreadGoalUsage(
		context.Background(), threadID, timeDeltaSeconds, tokenDelta,
		mode, &expectedGoalID,
	)
	if err != nil {
		slog.Warn("failed to account thread goal usage", "thread_id", threadID, "turn_id", turnID, "error", err)
		return
	}
	if outcome != nil && outcome.Updated && outcome.Goal != nil {
		r.emitStateThreadGoalUpdate(outcome.Goal, turnID, snapshot.ConnectionID, telemetry.GoalEventKindUsageAccounted)
	}
	if turnErr == nil && outcome != nil && outcome.Goal != nil && outcome.Goal.Status == state.ThreadGoalActive {
		r.markGoalIdleActive(outcome.Goal.GoalID)
	}
	// Rust #41454: account the ending turn (above) before the active-goal
	// stop. When a thread's active goal reaches three consecutive turns of
	// handler-executed `exec` failures without an intervening successful tool,
	// block the goal with the same accounting/status ordering as
	// ActiveGoalStopReason::ExecutionUnavailable. The streak resets when any
	// tool succeeds, when the active goal changes, or when the goal is cleared.
	execFailureGoal := r.advanceGoalExecutionFailure(threadID, snapshot.GoalID, snapshot)
	if execFailureGoal != "" {
		current, getErr := r.services.StateRuntime.GetThreadGoal(context.Background(), threadID)
		if getErr == nil && current != nil && current.GoalID == execFailureGoal &&
			(current.Status == state.ThreadGoalActive || current.Status == state.ThreadGoalBudgetLimited) {
			status := state.ThreadGoalBlocked
			updated, updateErr := r.services.StateRuntime.UpdateThreadGoal(context.Background(), threadID, state.GoalUpdate{
				Status: &status, ExpectedGoalID: &execFailureGoal,
			})
			if updateErr != nil {
				slog.Warn("failed to block thread goal after repeated execution failures", "thread_id", threadID, "turn_id", turnID, "error", updateErr)
			} else if updated != nil && updated.Status != current.Status {
				r.emitStateThreadGoalUpdate(updated, turnID, snapshot.ConnectionID, telemetry.GoalEventKindStatusChanged)
			}
			// The goal was blocked, ending the active goal; skip the turn-error
			// disposition below (there is no longer an active goal to stop).
			r.resetGoalExecutionFailure(threadID)
			return
		}
	}
	if turnErr == nil || finishMode == state.GoalAccountingActiveOrComplete {
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
	var turnIDPtr *string
	if turnIDCopy != "" {
		turnIDPtr = &turnIDCopy
	}
	r.notify(NotificationThreadGoalUpdated, &GoalUpdatedNotification{ThreadID: apiGoal.ThreadID, TurnID: turnIDPtr, Goal: *apiGoal})
	r.emitGoalAnalyticsEvent(context.Background(), connectionID, nil, apiGoal, analyticsKind, turnIDPtr)
}

// continueThreadGoalIfIdle starts an automatic continuation turn when the
// thread is idle and its persisted goal is active. It is invoked both after
// active goal mutations and from the thread idle transition so the agent
// continues working toward the objective across turns without requiring the
// user to submit a follow-up prompt.
func (r *RuntimeRouter) continueThreadGoalIfIdle(threadID string) {
	if r == nil || r.threads == nil || r.threads.IsClosing() {
		return
	}
	r.goalStateMu.Lock()
	defer r.goalStateMu.Unlock()
	threadID = strings.TrimSpace(threadID)
	if threadID == "" || r.services.ThreadRouter == nil || r.services.ThreadRouter.store == nil {
		return
	}
	if r.threads.ActiveTurn(threadID) != nil {
		return
	}
	if status := r.requireThreadStatus().LoadedStatusForThread(threadID); status.Type != IdleStatus().Type {
		return
	}

	ctx := context.Background()
	var promptGoal *prompt.Goal
	if r.services.StateRuntime != nil {
		deferred, err := r.services.StateRuntime.HasThreadGoalContinuationDeferral(ctx, threadID)
		if err != nil {
			slog.Warn("failed to read thread goal continuation deferral", "thread_id", threadID, "error", err)
			return
		}
		if deferred {
			return
		}
		goal, err := r.services.StateRuntime.GetThreadGoal(ctx, threadID)
		if err != nil {
			slog.Warn("failed to read thread goal for continuation", "thread_id", threadID, "error", err)
			return
		}
		if goal == nil || goal.Status != state.ThreadGoalActive {
			return
		}
		r.markGoalIdleActive(goal.GoalID)
		promptGoal = goalContinuationPrompt(goal)
	} else {
		record, err := r.threadRecord(session.ThreadID(threadID), true, false)
		if err != nil {
			slog.Warn("failed to read thread record for goal continuation", "thread_id", threadID, "error", err)
			return
		}
		goal, found, err := goalFromRecord(record)
		if err != nil {
			slog.Warn("failed to read legacy thread goal for continuation", "thread_id", threadID, "error", err)
			return
		}
		if !found || goal == nil || goal.Status != GoalActive {
			return
		}
		promptGoal = &prompt.Goal{
			Objective:   goal.Objective,
			TokenBudget: cloneInt64PtrAppserver(goal.TokenBudget),
			TokensUsed:  goal.TokensUsed,
		}
	}

	params := &turn.TurnStartParams{
		ThreadID: threadID,
		AdditionalContext: map[string]turn.AdditionalContextEntry{
			"goal": {
				Kind:  turn.AdditionalContextApplication,
				Value: prompt.Continuation(promptGoal),
			},
		},
	}
	if err := r.startGoalContinuationTurn(params); err != nil {
		slog.Warn("failed to start automatic goal continuation", "thread_id", threadID, "error", err)
	}
}

func goalContinuationPrompt(goal *state.ThreadGoal) *prompt.Goal {
	if goal == nil {
		return nil
	}
	return &prompt.Goal{
		Objective:       goal.Objective,
		TokenBudget:     cloneInt64PtrAppserver(goal.TokenBudget),
		TokensUsed:      goal.TokensUsed,
		TimeUsedSeconds: goal.TimeUsedSeconds,
	}
}

func promptGoalFromAPI(goal Goal) *prompt.Goal {
	return &prompt.Goal{
		Objective:       goal.Objective,
		TokenBudget:     cloneInt64PtrAppserver(goal.TokenBudget),
		TokensUsed:      goal.TokensUsed,
		TimeUsedSeconds: goal.TimeUsedSeconds,
	}
}

func (r *RuntimeRouter) applyGoalActiveRuntimeEffects(threadID string, goal Goal) {
	if r == nil {
		return
	}
	if goal.Status != GoalActive {
		r.clearActiveGoalStateForThread(threadID)
		return
	}
	if active := r.threads.ActiveTurn(threadID); active != nil && strings.TrimSpace(active.TurnID) != "" {
		r.ensureStateThreadGoalTurnActive(threadID, strings.TrimSpace(active.TurnID), goal.GoalID)
		item := modelInputTextMessage("developer", prompt.ObjectiveUpdated(promptGoalFromAPI(goal)))
		if err := r.requireSteerMailbox().Enqueue(&turn.SteerEnqueueParams{
			ThreadID:   threadID,
			TurnID:     strings.TrimSpace(active.TurnID),
			InputItems: []any{item},
		}); err != nil {
			slog.Warn("failed to enqueue goal objective update steering", "thread_id", threadID, "error", err)
		}
		r.continueThreadGoalIfIdle(threadID)
		return
	}
	r.markGoalIdleActive(goal.GoalID)
	r.continueThreadGoalIfIdle(threadID)
}

func (r *RuntimeRouter) startGoalContinuationTurn(params *turn.TurnStartParams) error {
	if r == nil || params == nil {
		return fmt.Errorf("%w: goal continuation turn params are required", ErrInvalidRequest)
	}
	r.inheritTurnEnvironmentSelections(params)
	if err := r.prepareTurnStartParams(params); err != nil {
		return err
	}
	if err := r.validateTurnStartEnvironments(params); err != nil {
		return err
	}
	if err := params.Validate(); err != nil {
		return err
	}
	if err := r.runPendingSessionStartHook(context.Background(), params); err != nil {
		return err
	}
	reservedRuntime := false
	if r.hasRuntimeThreadStore() {
		if err := r.reserveRuntimeThread(params.ThreadID); err != nil {
			return err
		}
		reservedRuntime = true
	}
	response, err := r.requireTurns().Start(params)
	if err != nil {
		if reservedRuntime {
			r.clearActiveRuntimeTurn(params.ThreadID, "")
		}
		return err
	}
	_ = r.persistTurnStartRuntimeWorkspaceRoots(params)
	_ = r.persistTurnEnvironmentSelections(params)
	r.startTurnRuntimeAsync(params, response, "")
	return nil
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
