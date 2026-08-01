package state

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	MemoryJobKindStage1            = "memory_stage1"
	MemoryJobKindConsolidateGlobal = "memory_consolidate_global"
	MemoryConsolidationJobKey      = "global"
	memoryDefaultRetryRemaining    = int64(3)
	memoryPhase2CooldownSeconds    = int64(6 * 60 * 60)
	memoryPhase2SelectionPageSize  = 512
)

type Stage1JobClaimOutcome string

const (
	Stage1JobClaimed               Stage1JobClaimOutcome = "claimed"
	Stage1JobSkippedUpToDate       Stage1JobClaimOutcome = "skipped_up_to_date"
	Stage1JobSkippedRunning        Stage1JobClaimOutcome = "skipped_running"
	Stage1JobSkippedRetryBackoff   Stage1JobClaimOutcome = "skipped_retry_backoff"
	Stage1JobSkippedRetryExhausted Stage1JobClaimOutcome = "skipped_retry_exhausted"
)

type Stage1JobClaimResult struct {
	Outcome        Stage1JobClaimOutcome
	OwnershipToken string
}

type Phase2JobClaimOutcome string

const (
	Phase2JobClaimed                 Phase2JobClaimOutcome = "claimed"
	Phase2JobSkippedRetryUnavailable Phase2JobClaimOutcome = "skipped_retry_unavailable"
	Phase2JobSkippedCooldown         Phase2JobClaimOutcome = "skipped_cooldown"
	Phase2JobSkippedRunning          Phase2JobClaimOutcome = "skipped_running"
)

type Phase2JobClaimResult struct {
	Outcome        Phase2JobClaimOutcome
	OwnershipToken string
	InputWatermark int64
}

type MemoryThread struct {
	ID            string
	RolloutPath   string
	UpdatedAt     time.Time
	Source        string
	CWD           string
	GitBranch     string
	Title         string
	Preview       string
	ModelProvider string
	Model         string
	MemoryMode    string
}

type Stage1Output struct {
	ThreadID        string
	RolloutPath     string
	SourceUpdatedAt time.Time
	RawMemory       string
	RolloutSummary  string
	RolloutSlug     string
	CWD             string
	GitBranch       string
	GeneratedAt     time.Time
}

type Stage1StartupClaimParams struct {
	ScanLimit           int
	MaxClaimed          int
	MaxAgeDays          int64
	MinRolloutIdleHours int64
	AllowedSources      []string
	LeaseSeconds        int64
	MaxRunningJobs      int
}

type Stage1StartupClaim struct {
	Thread         MemoryThread
	OwnershipToken string
}

func (r *StateRuntime) ClearMemoryData(ctx context.Context) error {
	if err := r.requireMemoriesRuntime(); err != nil {
		return err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	tx, err := r.memoriesDB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin memory reset: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `DELETE FROM stage1_outputs`); err != nil {
		return fmt.Errorf("clear stage1 outputs: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM jobs WHERE kind = ? OR kind = ?`, MemoryJobKindStage1, MemoryJobKindConsolidateGlobal); err != nil {
		return fmt.Errorf("clear memory jobs: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit memory reset: %w", err)
	}
	return nil
}

func (r *StateRuntime) RecordStage1OutputUsage(ctx context.Context, threadIDs []string) (int64, error) {
	if err := r.requireMemoriesRuntime(); err != nil {
		return 0, err
	}
	ids := compactThreadIDs(threadIDs)
	if len(ids) == 0 {
		return 0, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	tx, err := r.memoriesDB.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()
	now := time.Now().UTC().Unix()
	var updated int64
	for _, threadID := range ids {
		result, err := tx.ExecContext(ctx, `
UPDATE stage1_outputs
SET usage_count = COALESCE(usage_count, 0) + 1, last_usage = ?
WHERE thread_id = ?`, now, threadID)
		if err != nil {
			return 0, fmt.Errorf("record memory usage for %s: %w", threadID, err)
		}
		rows, err := result.RowsAffected()
		if err != nil {
			return 0, err
		}
		updated += rows
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return updated, nil
}

func (r *StateRuntime) ClaimStage1JobsForStartup(ctx context.Context, currentThreadID string, params Stage1StartupClaimParams) ([]Stage1StartupClaim, error) {
	if err := r.requireMemoriesRuntime(); err != nil {
		return nil, err
	}
	currentThreadID = strings.TrimSpace(currentThreadID)
	if currentThreadID == "" {
		return nil, errors.New("current thread id is required")
	}
	if params.ScanLimit <= 0 || params.MaxClaimed <= 0 {
		return []Stage1StartupClaim{}, nil
	}
	if params.MaxRunningJobs <= 0 {
		params.MaxRunningJobs = params.MaxClaimed
	}
	if ctx == nil {
		ctx = context.Background()
	}
	now := time.Now().UTC()
	maxAgeCutoff := now.Add(-time.Duration(max(params.MaxAgeDays, 0)) * 24 * time.Hour).UnixMilli()
	idleCutoff := now.Add(-time.Duration(max(params.MinRolloutIdleHours, 0)) * time.Hour).UnixMilli()
	query := `
SELECT id, rollout_path, COALESCE(updated_at_ms, updated_at * 1000), source,
       cwd, git_branch, title, preview, model_provider, model, memory_mode
FROM threads
WHERE archived = 0 AND preview <> '' AND memory_mode = 'enabled'
  AND id != ? AND COALESCE(updated_at_ms, updated_at * 1000) >= ?
  AND COALESCE(updated_at_ms, updated_at * 1000) <= ?`
	args := []any{currentThreadID, maxAgeCutoff, idleCutoff}
	if len(params.AllowedSources) > 0 {
		query += ` AND source IN (` + sqlPlaceholders(len(params.AllowedSources)) + `)`
		for _, source := range params.AllowedSources {
			args = append(args, source)
		}
	}
	query += ` ORDER BY COALESCE(updated_at_ms, updated_at * 1000) DESC LIMIT ?`
	args = append(args, params.ScanLimit)
	rows, err := r.stateDB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("scan memory stage1 candidates: %w", err)
	}
	var candidates []MemoryThread
	for rows.Next() {
		var item MemoryThread
		var updatedAtMS int64
		var cwd, branch, title, preview, provider, model, mode sql.NullString
		if err := rows.Scan(&item.ID, &item.RolloutPath, &updatedAtMS, &item.Source, &cwd, &branch, &title, &preview, &provider, &model, &mode); err != nil {
			_ = rows.Close()
			return nil, err
		}
		item.UpdatedAt = time.UnixMilli(updatedAtMS).UTC()
		item.CWD, item.GitBranch, item.Title, item.Preview = cwd.String, branch.String, title.String, preview.String
		item.ModelProvider, item.Model, item.MemoryMode = provider.String, model.String, mode.String
		candidates = append(candidates, item)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	claimed := make([]Stage1StartupClaim, 0, params.MaxClaimed)
	for _, candidate := range candidates {
		if len(claimed) >= params.MaxClaimed {
			break
		}
		needsUpdate, err := r.stage1SourceNeedsUpdate(ctx, candidate.ID, candidate.UpdatedAt.Unix())
		if err != nil {
			return nil, err
		}
		if !needsUpdate {
			continue
		}
		result, err := r.TryClaimStage1Job(ctx, candidate.ID, currentThreadID, candidate.UpdatedAt.Unix(), params.LeaseSeconds, params.MaxRunningJobs)
		if err != nil {
			return nil, err
		}
		if result.Outcome == Stage1JobClaimed {
			claimed = append(claimed, Stage1StartupClaim{Thread: candidate, OwnershipToken: result.OwnershipToken})
		}
	}
	return claimed, nil
}

func (r *StateRuntime) ListStage1OutputsForGlobal(ctx context.Context, limit int) ([]Stage1Output, error) {
	if limit <= 0 {
		return []Stage1Output{}, nil
	}
	return r.listHydratedStage1Outputs(ctx, `
SELECT thread_id, source_updated_at, raw_memory, rollout_summary, rollout_slug, generated_at
FROM stage1_outputs
WHERE length(trim(raw_memory)) > 0 OR length(trim(rollout_summary)) > 0
ORDER BY source_updated_at DESC, thread_id DESC`, nil, limit)
}

func (r *StateRuntime) PruneStage1OutputsForRetention(ctx context.Context, maxUnusedDays int64, limit int) (int64, error) {
	if err := r.requireMemoriesRuntime(); err != nil {
		return 0, err
	}
	if limit <= 0 {
		return 0, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	cutoff := time.Now().UTC().Add(-time.Duration(max(maxUnusedDays, 0)) * 24 * time.Hour).Unix()
	result, err := r.memoriesDB.ExecContext(ctx, `
DELETE FROM stage1_outputs
WHERE thread_id IN (
    SELECT thread_id FROM stage1_outputs
    WHERE selected_for_phase2 = 0 AND COALESCE(last_usage, source_updated_at) < ?
    ORDER BY COALESCE(last_usage, source_updated_at), source_updated_at, thread_id
    LIMIT ?
)`, cutoff, limit)
	if err != nil {
		return 0, fmt.Errorf("prune stage1 outputs: %w", err)
	}
	return result.RowsAffected()
}

func (r *StateRuntime) GetPhase2InputSelection(ctx context.Context, limit int, maxUnusedDays int64) ([]Stage1Output, error) {
	if err := r.requireMemoriesRuntime(); err != nil {
		return nil, err
	}
	if limit <= 0 {
		return []Stage1Output{}, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	cutoff := time.Now().UTC().Add(-time.Duration(max(maxUnusedDays, 0)) * 24 * time.Hour).Unix()
	pageSize := min(max(limit, 1), memoryPhase2SelectionPageSize)
	var keys []memoryStage1Key
	for offset := 0; len(keys) < limit; offset += pageSize {
		rows, err := r.memoriesDB.QueryContext(ctx, `
SELECT thread_id, source_updated_at
FROM stage1_outputs
WHERE (length(trim(raw_memory)) > 0 OR length(trim(rollout_summary)) > 0)
  AND ((last_usage IS NOT NULL AND last_usage >= ?)
       OR (last_usage IS NULL AND source_updated_at >= ?))
ORDER BY COALESCE(usage_count, 0) DESC,
         COALESCE(last_usage, source_updated_at) DESC,
         source_updated_at DESC, thread_id DESC
LIMIT ? OFFSET ?`, cutoff, cutoff, pageSize, offset)
		if err != nil {
			return nil, err
		}
		count := 0
		for rows.Next() {
			count++
			var key memoryStage1Key
			if err := rows.Scan(&key.threadID, &key.sourceUpdatedAt); err != nil {
				_ = rows.Close()
				return nil, err
			}
			if _, ok, err := r.enabledMemoryThread(ctx, key.threadID); err != nil {
				_ = rows.Close()
				return nil, err
			} else if ok {
				keys = append(keys, key)
				if len(keys) >= limit {
					break
				}
			}
		}
		_ = rows.Close()
		if count < pageSize {
			break
		}
	}
	selected := make([]Stage1Output, 0, len(keys))
	for _, key := range keys {
		row := r.memoriesDB.QueryRowContext(ctx, `
SELECT thread_id, source_updated_at, raw_memory, rollout_summary, rollout_slug, generated_at
FROM stage1_outputs WHERE thread_id = ? AND source_updated_at = ?`, key.threadID, key.sourceUpdatedAt)
		output, ok, err := r.hydrateStage1Output(ctx, row)
		if err != nil {
			return nil, err
		}
		if ok {
			selected = append(selected, output)
		}
	}
	sortStage1OutputsByThreadID(selected)
	return selected, nil
}

func (r *StateRuntime) MarkThreadMemoryModePolluted(ctx context.Context, threadID string) (bool, error) {
	if err := r.requireMemoriesRuntime(); err != nil {
		return false, err
	}
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return false, errors.New("thread id is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	var selected int64
	err := r.memoriesDB.QueryRowContext(ctx, `SELECT selected_for_phase2 FROM stage1_outputs WHERE thread_id = ?`, threadID).Scan(&selected)
	if err != nil && err != sql.ErrNoRows {
		return false, err
	}
	result, err := r.stateDB.ExecContext(ctx, `UPDATE threads SET memory_mode = 'polluted' WHERE id = ? AND memory_mode != 'polluted'`, threadID)
	if err != nil {
		return false, err
	}
	if selected != 0 {
		if err := r.EnqueueGlobalConsolidation(ctx, time.Now().UTC().Unix()); err != nil {
			return false, err
		}
	}
	rows, err := result.RowsAffected()
	return rows > 0, err
}

type memoryStage1Key struct {
	threadID        string
	sourceUpdatedAt int64
}

func (r *StateRuntime) stage1SourceNeedsUpdate(ctx context.Context, threadID string, sourceUpdatedAt int64) (bool, error) {
	var watermark int64
	err := r.memoriesDB.QueryRowContext(ctx, `SELECT source_updated_at FROM stage1_outputs WHERE thread_id = ?`, threadID).Scan(&watermark)
	if err == nil && watermark >= sourceUpdatedAt {
		return false, nil
	}
	if err != nil && err != sql.ErrNoRows {
		return false, err
	}
	var lastSuccess sql.NullInt64
	err = r.memoriesDB.QueryRowContext(ctx, `SELECT last_success_watermark FROM jobs WHERE kind = ? AND job_key = ?`, MemoryJobKindStage1, threadID).Scan(&lastSuccess)
	if err == nil && lastSuccess.Valid && lastSuccess.Int64 >= sourceUpdatedAt {
		return false, nil
	}
	if err != nil && err != sql.ErrNoRows {
		return false, err
	}
	return true, nil
}

func (r *StateRuntime) listHydratedStage1Outputs(ctx context.Context, query string, args []any, limit int) ([]Stage1Output, error) {
	if err := r.requireMemoriesRuntime(); err != nil {
		return nil, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	rows, err := r.memoriesDB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	outputs := make([]Stage1Output, 0, limit)
	for rows.Next() {
		output, ok, err := r.hydrateStage1Output(ctx, rows)
		if err != nil {
			return nil, err
		}
		if ok {
			outputs = append(outputs, output)
			if len(outputs) >= limit {
				break
			}
		}
	}
	return outputs, rows.Err()
}

type memoryStage1Scanner interface {
	Scan(...any) error
}

func (r *StateRuntime) hydrateStage1Output(ctx context.Context, scanner memoryStage1Scanner) (Stage1Output, bool, error) {
	var output Stage1Output
	var sourceUpdatedAt, generatedAt int64
	var rolloutSlug sql.NullString
	if err := scanner.Scan(&output.ThreadID, &sourceUpdatedAt, &output.RawMemory, &output.RolloutSummary, &rolloutSlug, &generatedAt); err != nil {
		if err == sql.ErrNoRows {
			return Stage1Output{}, false, nil
		}
		return Stage1Output{}, false, err
	}
	thread, ok, err := r.enabledMemoryThread(ctx, output.ThreadID)
	if err != nil || !ok {
		return Stage1Output{}, false, err
	}
	output.RolloutPath = thread.RolloutPath
	output.CWD = thread.CWD
	output.GitBranch = thread.GitBranch
	output.RolloutSlug = rolloutSlug.String
	output.SourceUpdatedAt = time.Unix(sourceUpdatedAt, 0).UTC()
	output.GeneratedAt = time.Unix(generatedAt, 0).UTC()
	return output, true, nil
}

func (r *StateRuntime) enabledMemoryThread(ctx context.Context, threadID string) (MemoryThread, bool, error) {
	var item MemoryThread
	var updatedAtMS int64
	var cwd, branch, title, preview, provider, model, mode sql.NullString
	err := r.stateDB.QueryRowContext(ctx, `
SELECT id, rollout_path, COALESCE(updated_at_ms, updated_at * 1000), source,
       cwd, git_branch, title, preview, model_provider, model, memory_mode
FROM threads WHERE id = ? AND memory_mode = 'enabled'`, threadID).Scan(
		&item.ID, &item.RolloutPath, &updatedAtMS, &item.Source, &cwd, &branch,
		&title, &preview, &provider, &model, &mode,
	)
	if err == sql.ErrNoRows {
		return MemoryThread{}, false, nil
	}
	if err != nil {
		return MemoryThread{}, false, err
	}
	item.UpdatedAt = time.UnixMilli(updatedAtMS).UTC()
	item.CWD, item.GitBranch, item.Title, item.Preview = cwd.String, branch.String, title.String, preview.String
	item.ModelProvider, item.Model, item.MemoryMode = provider.String, model.String, mode.String
	return item, true, nil
}

func (r *StateRuntime) requireMemoriesRuntime() error {
	if r == nil || r.memoriesDB == nil || r.stateDB == nil {
		return errors.New("state runtime memories are unavailable")
	}
	return nil
}

func sqlPlaceholders(count int) string {
	if count <= 0 {
		return ""
	}
	return strings.TrimSuffix(strings.Repeat("?,", count), ",")
}

func sortStage1OutputsByThreadID(values []Stage1Output) {
	for i := 1; i < len(values); i++ {
		for j := i; j > 0 && values[j].ThreadID < values[j-1].ThreadID; j-- {
			values[j], values[j-1] = values[j-1], values[j]
		}
	}
}
