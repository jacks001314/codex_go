package state

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

type ThreadGoalStatus string

const (
	ThreadGoalActive        ThreadGoalStatus = "active"
	ThreadGoalPaused        ThreadGoalStatus = "paused"
	ThreadGoalBlocked       ThreadGoalStatus = "blocked"
	ThreadGoalUsageLimited  ThreadGoalStatus = "usage_limited"
	ThreadGoalBudgetLimited ThreadGoalStatus = "budget_limited"
	ThreadGoalComplete      ThreadGoalStatus = "complete"
)

func (s ThreadGoalStatus) Valid() bool {
	switch s {
	case ThreadGoalActive, ThreadGoalPaused, ThreadGoalBlocked, ThreadGoalUsageLimited, ThreadGoalBudgetLimited, ThreadGoalComplete:
		return true
	default:
		return false
	}
}

type ThreadGoal struct {
	ThreadID        string
	GoalID          string
	Objective       string
	Status          ThreadGoalStatus
	TokenBudget     *int64
	TokensUsed      int64
	TimeUsedSeconds int64
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

type GoalUpdate struct {
	Objective      *string
	Status         *ThreadGoalStatus
	TokenBudget    *int64
	TokenBudgetSet bool
	ExpectedGoalID *string
}

type GoalAccountingMode string

const (
	GoalAccountingActiveStatusOnly GoalAccountingMode = "active_status_only"
	GoalAccountingActiveOnly       GoalAccountingMode = "active_only"
	GoalAccountingActiveOrComplete GoalAccountingMode = "active_or_complete"
	GoalAccountingActiveOrStopped  GoalAccountingMode = "active_or_stopped"
)

type GoalAccountingOutcome struct {
	Goal    *ThreadGoal
	Updated bool
}

const threadGoalColumns = `
    thread_id,
    goal_id,
    objective,
    status,
    token_budget,
    tokens_used,
    time_used_seconds,
    created_at_ms,
    updated_at_ms`

func (r *StateRuntime) GetThreadGoal(ctx context.Context, threadID string) (*ThreadGoal, error) {
	if err := r.requireGoalsDB(); err != nil {
		return nil, err
	}
	ctx = nonNilContext(ctx)
	row := r.goalsDB.QueryRowContext(ctx, `SELECT `+threadGoalColumns+` FROM thread_goals WHERE thread_id = ?`, strings.TrimSpace(threadID))
	goal, err := scanThreadGoal(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read thread goal: %w", err)
	}
	return goal, nil
}

func (r *StateRuntime) ReplaceThreadGoalSnapshot(ctx context.Context, goal *ThreadGoal) error {
	if err := r.requireGoalsDB(); err != nil {
		return err
	}
	if goal == nil {
		return errors.New("thread goal snapshot is nil")
	}
	ctx = nonNilContext(ctx)
	tx, err := r.goalsDB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin thread goal snapshot replacement: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `
INSERT INTO thread_goals (`+threadGoalColumns+`)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(thread_id) DO UPDATE SET
    goal_id = excluded.goal_id,
    objective = excluded.objective,
    status = excluded.status,
    token_budget = excluded.token_budget,
    tokens_used = excluded.tokens_used,
    time_used_seconds = excluded.time_used_seconds,
    created_at_ms = excluded.created_at_ms,
    updated_at_ms = excluded.updated_at_ms`,
		strings.TrimSpace(goal.ThreadID), goal.GoalID, goal.Objective, string(goal.Status), nullableInt64(goal.TokenBudget),
		goal.TokensUsed, goal.TimeUsedSeconds, goal.CreatedAt.UnixMilli(), goal.UpdatedAt.UnixMilli()); err != nil {
		return fmt.Errorf("replace thread goal snapshot: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO thread_goal_continuation_deferrals (thread_id)
VALUES (?)
ON CONFLICT(thread_id) DO NOTHING`, strings.TrimSpace(goal.ThreadID)); err != nil {
		return fmt.Errorf("defer inherited thread goal continuation: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit thread goal snapshot replacement: %w", err)
	}
	return nil
}

func (r *StateRuntime) HasThreadGoalContinuationDeferral(ctx context.Context, threadID string) (bool, error) {
	if err := r.requireGoalsDB(); err != nil {
		return false, err
	}
	ctx = nonNilContext(ctx)
	var exists bool
	if err := r.goalsDB.QueryRowContext(ctx, `
SELECT EXISTS(
    SELECT 1 FROM thread_goal_continuation_deferrals WHERE thread_id = ?
)`, strings.TrimSpace(threadID)).Scan(&exists); err != nil {
		return false, fmt.Errorf("read thread goal continuation deferral: %w", err)
	}
	return exists, nil
}

func (r *StateRuntime) ClearThreadGoalContinuationDeferral(ctx context.Context, threadID string) error {
	if err := r.requireGoalsDB(); err != nil {
		return err
	}
	ctx = nonNilContext(ctx)
	if _, err := r.goalsDB.ExecContext(ctx, `DELETE FROM thread_goal_continuation_deferrals WHERE thread_id = ?`, strings.TrimSpace(threadID)); err != nil {
		return fmt.Errorf("clear thread goal continuation deferral: %w", err)
	}
	return nil
}

func (r *StateRuntime) ReplaceThreadGoal(ctx context.Context, threadID, objective string, status ThreadGoalStatus, tokenBudget *int64) (*ThreadGoal, error) {
	if err := r.requireGoalsDB(); err != nil {
		return nil, err
	}
	ctx = nonNilContext(ctx)
	nowMS := time.Now().UTC().UnixMilli()
	status = threadGoalStatusAfterBudgetLimit(status, 0, tokenBudget)
	row := r.goalsDB.QueryRowContext(ctx, `
INSERT INTO thread_goals (`+threadGoalColumns+`)
VALUES (?, ?, ?, ?, ?, 0, 0, ?, ?)
ON CONFLICT(thread_id) DO UPDATE SET
    goal_id = excluded.goal_id,
    objective = excluded.objective,
    status = excluded.status,
    token_budget = excluded.token_budget,
    tokens_used = 0,
    time_used_seconds = 0,
    created_at_ms = excluded.created_at_ms,
    updated_at_ms = excluded.updated_at_ms
RETURNING `+threadGoalColumns,
		strings.TrimSpace(threadID), uuid.NewString(), objective, string(status), nullableInt64(tokenBudget), nowMS, nowMS)
	goal, err := scanThreadGoal(row)
	if err != nil {
		return nil, fmt.Errorf("replace thread goal: %w", err)
	}
	return goal, nil
}

func (r *StateRuntime) InsertThreadGoal(ctx context.Context, threadID, objective string, status ThreadGoalStatus, tokenBudget *int64) (*ThreadGoal, error) {
	if err := r.requireGoalsDB(); err != nil {
		return nil, err
	}
	ctx = nonNilContext(ctx)
	nowMS := time.Now().UTC().UnixMilli()
	status = threadGoalStatusAfterBudgetLimit(status, 0, tokenBudget)
	row := r.goalsDB.QueryRowContext(ctx, `
INSERT INTO thread_goals (`+threadGoalColumns+`)
VALUES (?, ?, ?, ?, ?, 0, 0, ?, ?)
ON CONFLICT(thread_id) DO UPDATE SET
    goal_id = excluded.goal_id,
    objective = excluded.objective,
    status = excluded.status,
    token_budget = excluded.token_budget,
    tokens_used = 0,
    time_used_seconds = 0,
    created_at_ms = excluded.created_at_ms,
    updated_at_ms = excluded.updated_at_ms
WHERE thread_goals.status = 'complete'
RETURNING `+threadGoalColumns,
		strings.TrimSpace(threadID), uuid.NewString(), objective, string(status), nullableInt64(tokenBudget), nowMS, nowMS)
	goal, err := scanThreadGoal(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("insert thread goal: %w", err)
	}
	return goal, nil
}

func (r *StateRuntime) UpdateThreadGoal(ctx context.Context, threadID string, update GoalUpdate) (*ThreadGoal, error) {
	if err := r.requireGoalsDB(); err != nil {
		return nil, err
	}
	ctx = nonNilContext(ctx)
	if update.Status == nil && !update.TokenBudgetSet && update.Objective == nil {
		goal, err := r.GetThreadGoal(ctx, threadID)
		if err != nil || goal == nil || update.ExpectedGoalID == nil || goal.GoalID == *update.ExpectedGoalID {
			return goal, err
		}
		return nil, nil
	}

	nowMS := time.Now().UTC().UnixMilli()
	expectedID := nullableStringPointer(update.ExpectedGoalID)
	argsSuffix := []any{nowMS, strings.TrimSpace(threadID), expectedID, expectedID}
	var query string
	var args []any
	switch {
	case update.Status != nil && update.TokenBudgetSet:
		query = `UPDATE thread_goals SET
    objective = COALESCE(?, objective),
    status = CASE
        WHEN status = ? AND ? IN (?, ?) THEN status
        WHEN ? = 'active' AND ? IS NOT NULL AND tokens_used >= ? THEN ?
        ELSE ?
    END,
    token_budget = ?,
    updated_at_ms = ?
WHERE thread_id = ? AND (? IS NULL OR goal_id = ?)`
		budget := nullableInt64(update.TokenBudget)
		args = []any{nullableStringPointer(update.Objective), string(ThreadGoalBudgetLimited), string(*update.Status), string(ThreadGoalPaused), string(ThreadGoalBlocked), string(*update.Status), budget, budget, string(ThreadGoalBudgetLimited), string(*update.Status), budget}
	case update.Status != nil:
		query = `UPDATE thread_goals SET
    objective = COALESCE(?, objective),
    status = CASE
        WHEN status = ? AND ? IN (?, ?) THEN status
        WHEN ? = 'active' AND token_budget IS NOT NULL AND tokens_used >= token_budget THEN ?
        ELSE ?
    END,
    updated_at_ms = ?
WHERE thread_id = ? AND (? IS NULL OR goal_id = ?)`
		args = []any{nullableStringPointer(update.Objective), string(ThreadGoalBudgetLimited), string(*update.Status), string(ThreadGoalPaused), string(ThreadGoalBlocked), string(*update.Status), string(ThreadGoalBudgetLimited), string(*update.Status)}
	case update.TokenBudgetSet:
		query = `UPDATE thread_goals SET
    objective = COALESCE(?, objective),
    token_budget = ?,
    status = CASE
        WHEN status = 'active' AND ? IS NOT NULL AND tokens_used >= ? THEN ?
        ELSE status
    END,
    updated_at_ms = ?
WHERE thread_id = ? AND (? IS NULL OR goal_id = ?)`
		budget := nullableInt64(update.TokenBudget)
		args = []any{nullableStringPointer(update.Objective), budget, budget, budget, string(ThreadGoalBudgetLimited)}
	default:
		query = `UPDATE thread_goals SET objective = ?, updated_at_ms = ? WHERE thread_id = ? AND (? IS NULL OR goal_id = ?)`
		args = []any{*update.Objective}
	}
	args = append(args, argsSuffix...)
	result, err := r.goalsDB.ExecContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("update thread goal: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return nil, fmt.Errorf("read updated thread goal row count: %w", err)
	}
	if rows == 0 {
		return nil, nil
	}
	return r.GetThreadGoal(ctx, threadID)
}

func (r *StateRuntime) PauseActiveThreadGoal(ctx context.Context, threadID string) (*ThreadGoal, error) {
	return r.updateActiveThreadGoalStatus(ctx, threadID, ThreadGoalPaused)
}

func (r *StateRuntime) UsageLimitActiveThreadGoal(ctx context.Context, threadID string) (*ThreadGoal, error) {
	return r.updateActiveThreadGoalStatus(ctx, threadID, ThreadGoalUsageLimited)
}

func (r *StateRuntime) updateActiveThreadGoalStatus(ctx context.Context, threadID string, status ThreadGoalStatus) (*ThreadGoal, error) {
	if err := r.requireGoalsDB(); err != nil {
		return nil, err
	}
	ctx = nonNilContext(ctx)
	result, err := r.goalsDB.ExecContext(ctx, `
UPDATE thread_goals
SET status = ?, updated_at_ms = ?
WHERE thread_id = ?
  AND (status = 'active' OR (? = 'usage_limited' AND status = 'budget_limited'))`,
		string(status), time.Now().UTC().UnixMilli(), strings.TrimSpace(threadID), string(status))
	if err != nil {
		return nil, fmt.Errorf("update active thread goal status: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return nil, fmt.Errorf("read active thread goal row count: %w", err)
	}
	if rows == 0 {
		return nil, nil
	}
	return r.GetThreadGoal(ctx, threadID)
}

func (r *StateRuntime) DeleteThreadGoal(ctx context.Context, threadID string) (*ThreadGoal, error) {
	if err := r.requireGoalsDB(); err != nil {
		return nil, err
	}
	ctx = nonNilContext(ctx)
	row := r.goalsDB.QueryRowContext(ctx, `DELETE FROM thread_goals WHERE thread_id = ? RETURNING `+threadGoalColumns, strings.TrimSpace(threadID))
	goal, err := scanThreadGoal(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("delete thread goal: %w", err)
	}
	return goal, nil
}

func (r *StateRuntime) AccountThreadGoalUsage(ctx context.Context, threadID string, timeDeltaSeconds, tokenDelta int64, mode GoalAccountingMode, expectedGoalID *string) (*GoalAccountingOutcome, error) {
	if err := r.requireGoalsDB(); err != nil {
		return nil, err
	}
	ctx = nonNilContext(ctx)
	if timeDeltaSeconds < 0 {
		timeDeltaSeconds = 0
	}
	if tokenDelta < 0 {
		tokenDelta = 0
	}
	if timeDeltaSeconds == 0 && tokenDelta == 0 {
		goal, err := r.GetThreadGoal(ctx, threadID)
		return &GoalAccountingOutcome{Goal: goal}, err
	}
	const stopped = "status IN ('active', 'paused', 'blocked', 'usage_limited', 'budget_limited')"
	statusFilter, budgetFilter := "", ""
	switch mode {
	case GoalAccountingActiveStatusOnly:
		statusFilter, budgetFilter = "status = 'active'", "status = 'active'"
	case GoalAccountingActiveOnly:
		statusFilter, budgetFilter = "status IN ('active', 'budget_limited')", "status = 'active'"
	case GoalAccountingActiveOrComplete:
		statusFilter, budgetFilter = "status IN ('active', 'budget_limited', 'complete')", "status = 'active'"
	case GoalAccountingActiveOrStopped:
		statusFilter, budgetFilter = stopped, stopped
	default:
		return nil, fmt.Errorf("unsupported thread goal accounting mode %q", mode)
	}
	query := `UPDATE thread_goals SET
    time_used_seconds = time_used_seconds + ?,
    tokens_used = tokens_used + ?,
    status = CASE
        WHEN ` + budgetFilter + ` AND token_budget IS NOT NULL AND tokens_used + ? >= token_budget
        THEN ?
        ELSE status
    END,
    updated_at_ms = ?
WHERE thread_id = ? AND ` + statusFilter
	args := []any{timeDeltaSeconds, tokenDelta, tokenDelta, string(ThreadGoalBudgetLimited), time.Now().UTC().UnixMilli(), strings.TrimSpace(threadID)}
	if expectedGoalID != nil {
		query += ` AND goal_id = ?`
		args = append(args, *expectedGoalID)
	}
	query += ` RETURNING ` + threadGoalColumns
	goal, err := scanThreadGoal(r.goalsDB.QueryRowContext(ctx, query, args...))
	if errors.Is(err, sql.ErrNoRows) {
		current, getErr := r.GetThreadGoal(ctx, threadID)
		return &GoalAccountingOutcome{Goal: current}, getErr
	}
	if err != nil {
		return nil, fmt.Errorf("account thread goal usage: %w", err)
	}
	return &GoalAccountingOutcome{Goal: goal, Updated: true}, nil
}

type threadGoalScanner interface {
	Scan(dest ...any) error
}

func scanThreadGoal(row threadGoalScanner) (*ThreadGoal, error) {
	var goal ThreadGoal
	var status string
	var budget sql.NullInt64
	var createdMS, updatedMS int64
	if err := row.Scan(&goal.ThreadID, &goal.GoalID, &goal.Objective, &status, &budget, &goal.TokensUsed, &goal.TimeUsedSeconds, &createdMS, &updatedMS); err != nil {
		return nil, err
	}
	goal.Status = ThreadGoalStatus(status)
	if !goal.Status.Valid() {
		return nil, fmt.Errorf("unknown thread goal status %q", status)
	}
	if budget.Valid {
		value := budget.Int64
		goal.TokenBudget = &value
	}
	goal.CreatedAt = time.UnixMilli(createdMS).UTC()
	goal.UpdatedAt = time.UnixMilli(updatedMS).UTC()
	return &goal, nil
}

func threadGoalStatusAfterBudgetLimit(status ThreadGoalStatus, tokensUsed int64, tokenBudget *int64) ThreadGoalStatus {
	if status == ThreadGoalActive && tokenBudget != nil && tokensUsed >= *tokenBudget {
		return ThreadGoalBudgetLimited
	}
	return status
}

func (r *StateRuntime) requireGoalsDB() error {
	if r == nil || r.goalsDB == nil {
		return errors.New("goals state database is unavailable")
	}
	return nil
}

func nonNilContext(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return ctx
}
