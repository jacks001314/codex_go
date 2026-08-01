package state

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
)

func (r *StateRuntime) TryClaimStage1Job(ctx context.Context, threadID string, workerID string, sourceUpdatedAt int64, leaseSeconds int64, maxRunningJobs int) (Stage1JobClaimResult, error) {
	if err := r.requireMemoriesRuntime(); err != nil {
		return Stage1JobClaimResult{}, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	conn, err := beginMemoryImmediate(ctx, r.memoriesDB)
	if err != nil {
		return Stage1JobClaimResult{}, err
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = conn.ExecContext(context.Background(), `ROLLBACK`)
		}
		_ = conn.Close()
	}()

	var outputWatermark int64
	err = conn.QueryRowContext(ctx, `SELECT source_updated_at FROM stage1_outputs WHERE thread_id = ?`, threadID).Scan(&outputWatermark)
	if err == nil && outputWatermark >= sourceUpdatedAt {
		if err := commitMemoryImmediate(ctx, conn); err != nil {
			return Stage1JobClaimResult{}, err
		}
		committed = true
		return Stage1JobClaimResult{Outcome: Stage1JobSkippedUpToDate}, nil
	}
	if err != nil && err != sql.ErrNoRows {
		return Stage1JobClaimResult{}, err
	}
	var lastSuccess sql.NullInt64
	err = conn.QueryRowContext(ctx, `SELECT last_success_watermark FROM jobs WHERE kind = ? AND job_key = ?`, MemoryJobKindStage1, threadID).Scan(&lastSuccess)
	if err == nil && lastSuccess.Valid && lastSuccess.Int64 >= sourceUpdatedAt {
		if err := commitMemoryImmediate(ctx, conn); err != nil {
			return Stage1JobClaimResult{}, err
		}
		committed = true
		return Stage1JobClaimResult{Outcome: Stage1JobSkippedUpToDate}, nil
	}
	if err != nil && err != sql.ErrNoRows {
		return Stage1JobClaimResult{}, err
	}

	now := time.Now().UTC().Unix()
	leaseUntil := now + max(leaseSeconds, 0)
	ownershipToken := uuid.NewString()
	result, err := conn.ExecContext(ctx, `
INSERT INTO jobs (
    kind, job_key, status, worker_id, ownership_token, started_at, finished_at,
    lease_until, retry_at, retry_remaining, last_error, input_watermark, last_success_watermark
)
SELECT ?, ?, 'running', ?, ?, ?, NULL, ?, NULL, ?, NULL, ?, NULL
WHERE (
    SELECT COUNT(*) FROM jobs
    WHERE kind = ? AND status = 'running' AND lease_until IS NOT NULL AND lease_until > ?
) < ?
ON CONFLICT(kind, job_key) DO UPDATE SET
    status = 'running', worker_id = excluded.worker_id,
    ownership_token = excluded.ownership_token, started_at = excluded.started_at,
    finished_at = NULL, lease_until = excluded.lease_until, retry_at = NULL,
    retry_remaining = CASE
        WHEN excluded.input_watermark > COALESCE(jobs.input_watermark, -1) THEN ?
        ELSE jobs.retry_remaining
    END,
    last_error = NULL, input_watermark = excluded.input_watermark
WHERE
    (jobs.status != 'running' OR jobs.lease_until IS NULL OR jobs.lease_until <= excluded.started_at)
    AND (jobs.retry_at IS NULL OR jobs.retry_at <= excluded.started_at
         OR excluded.input_watermark > COALESCE(jobs.input_watermark, -1))
    AND (jobs.retry_remaining > 0
         OR excluded.input_watermark > COALESCE(jobs.input_watermark, -1))
    AND (
        SELECT COUNT(*) FROM jobs AS running_jobs
        WHERE running_jobs.kind = excluded.kind AND running_jobs.status = 'running'
          AND running_jobs.lease_until IS NOT NULL
          AND running_jobs.lease_until > excluded.started_at
          AND running_jobs.job_key != excluded.job_key
    ) < ?`,
		MemoryJobKindStage1, threadID, workerID, ownershipToken, now, leaseUntil,
		memoryDefaultRetryRemaining, sourceUpdatedAt, MemoryJobKindStage1, now,
		maxRunningJobs, memoryDefaultRetryRemaining, maxRunningJobs,
	)
	if err != nil {
		return Stage1JobClaimResult{}, fmt.Errorf("claim stage1 job %s: %w", threadID, err)
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return Stage1JobClaimResult{}, err
	}
	if rowsAffected > 0 {
		if err := commitMemoryImmediate(ctx, conn); err != nil {
			return Stage1JobClaimResult{}, err
		}
		committed = true
		return Stage1JobClaimResult{Outcome: Stage1JobClaimed, OwnershipToken: ownershipToken}, nil
	}

	var status string
	var existingLeaseUntil, retryAt sql.NullInt64
	var retryRemaining int64
	err = conn.QueryRowContext(ctx, `
SELECT status, lease_until, retry_at, retry_remaining
FROM jobs WHERE kind = ? AND job_key = ?`, MemoryJobKindStage1, threadID).Scan(
		&status, &existingLeaseUntil, &retryAt, &retryRemaining,
	)
	if err != nil && err != sql.ErrNoRows {
		return Stage1JobClaimResult{}, err
	}
	if err := commitMemoryImmediate(ctx, conn); err != nil {
		return Stage1JobClaimResult{}, err
	}
	committed = true
	switch {
	case err == nil && retryRemaining <= 0:
		return Stage1JobClaimResult{Outcome: Stage1JobSkippedRetryExhausted}, nil
	case err == nil && retryAt.Valid && retryAt.Int64 > now:
		return Stage1JobClaimResult{Outcome: Stage1JobSkippedRetryBackoff}, nil
	case err == nil && status == "running" && existingLeaseUntil.Valid && existingLeaseUntil.Int64 > now:
		return Stage1JobClaimResult{Outcome: Stage1JobSkippedRunning}, nil
	default:
		return Stage1JobClaimResult{Outcome: Stage1JobSkippedRunning}, nil
	}
}

func (r *StateRuntime) MarkStage1JobSucceeded(ctx context.Context, threadID string, ownershipToken string, sourceUpdatedAt int64, rawMemory string, rolloutSummary string, rolloutSlug *string) (bool, error) {
	if err := r.requireMemoriesRuntime(); err != nil {
		return false, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	tx, err := r.memoriesDB.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	now := time.Now().UTC().Unix()
	result, err := tx.ExecContext(ctx, `
UPDATE jobs SET status = 'done', finished_at = ?, lease_until = NULL,
    last_error = NULL, last_success_watermark = input_watermark
WHERE kind = ? AND job_key = ? AND status = 'running' AND ownership_token = ?`,
		now, MemoryJobKindStage1, threadID, ownershipToken)
	if err != nil {
		return false, err
	}
	rows, err := result.RowsAffected()
	if err != nil || rows == 0 {
		return false, err
	}
	var slug any
	if rolloutSlug != nil {
		slug = *rolloutSlug
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO stage1_outputs (
    thread_id, source_updated_at, raw_memory, rollout_summary, rollout_slug, generated_at
) VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT(thread_id) DO UPDATE SET
    source_updated_at = excluded.source_updated_at,
    raw_memory = excluded.raw_memory,
    rollout_summary = excluded.rollout_summary,
    rollout_slug = excluded.rollout_slug,
    generated_at = excluded.generated_at
WHERE excluded.source_updated_at >= stage1_outputs.source_updated_at`,
		threadID, sourceUpdatedAt, rawMemory, rolloutSummary, slug, now); err != nil {
		return false, err
	}
	if err := enqueueGlobalConsolidation(ctx, tx, sourceUpdatedAt); err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

func (r *StateRuntime) MarkStage1JobSucceededNoOutput(ctx context.Context, threadID string, ownershipToken string) (bool, error) {
	if err := r.requireMemoriesRuntime(); err != nil {
		return false, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	tx, err := r.memoriesDB.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	now := time.Now().UTC().Unix()
	result, err := tx.ExecContext(ctx, `
UPDATE jobs SET status = 'done', finished_at = ?, lease_until = NULL,
    last_error = NULL, last_success_watermark = input_watermark
WHERE kind = ? AND job_key = ? AND status = 'running' AND ownership_token = ?`,
		now, MemoryJobKindStage1, threadID, ownershipToken)
	if err != nil {
		return false, err
	}
	rows, err := result.RowsAffected()
	if err != nil || rows == 0 {
		return false, err
	}
	var sourceUpdatedAt int64
	if err := tx.QueryRowContext(ctx, `SELECT input_watermark FROM jobs WHERE kind = ? AND job_key = ? AND ownership_token = ?`, MemoryJobKindStage1, threadID, ownershipToken).Scan(&sourceUpdatedAt); err != nil {
		return false, err
	}
	deleted, err := tx.ExecContext(ctx, `DELETE FROM stage1_outputs WHERE thread_id = ?`, threadID)
	if err != nil {
		return false, err
	}
	deletedRows, err := deleted.RowsAffected()
	if err != nil {
		return false, err
	}
	if deletedRows > 0 {
		if err := enqueueGlobalConsolidation(ctx, tx, sourceUpdatedAt); err != nil {
			return false, err
		}
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

func (r *StateRuntime) MarkStage1JobFailed(ctx context.Context, threadID string, ownershipToken string, failureReason string, retryDelaySeconds int64) (bool, error) {
	if err := r.requireMemoriesRuntime(); err != nil {
		return false, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	now := time.Now().UTC().Unix()
	result, err := r.memoriesDB.ExecContext(ctx, `
UPDATE jobs SET status = 'error', finished_at = ?, lease_until = NULL,
    retry_at = ?, retry_remaining = retry_remaining - 1, last_error = ?
WHERE kind = ? AND job_key = ? AND status = 'running' AND ownership_token = ?`,
		now, now+max(retryDelaySeconds, 0), failureReason,
		MemoryJobKindStage1, threadID, ownershipToken)
	if err != nil {
		return false, err
	}
	rows, err := result.RowsAffected()
	return rows > 0, err
}

func (r *StateRuntime) EnqueueGlobalConsolidation(ctx context.Context, inputWatermark int64) error {
	if err := r.requireMemoriesRuntime(); err != nil {
		return err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return enqueueGlobalConsolidation(ctx, r.memoriesDB, inputWatermark)
}

func (r *StateRuntime) TryClaimGlobalPhase2Job(ctx context.Context, workerID string, leaseSeconds int64) (Phase2JobClaimResult, error) {
	if err := r.requireMemoriesRuntime(); err != nil {
		return Phase2JobClaimResult{}, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	conn, err := beginMemoryImmediate(ctx, r.memoriesDB)
	if err != nil {
		return Phase2JobClaimResult{}, err
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = conn.ExecContext(context.Background(), `ROLLBACK`)
		}
		_ = conn.Close()
	}()
	now := time.Now().UTC().Unix()
	leaseUntil := now + max(leaseSeconds, 0)
	cooldownCutoff := now - memoryPhase2CooldownSeconds
	ownershipToken := uuid.NewString()
	var status string
	var existingLease, retryAt, inputWatermark, finishedAt sql.NullInt64
	var lastError sql.NullString
	err = conn.QueryRowContext(ctx, `
SELECT status, lease_until, retry_at, input_watermark, finished_at, last_error
FROM jobs WHERE kind = ? AND job_key = ?`, MemoryJobKindConsolidateGlobal, MemoryConsolidationJobKey).Scan(
		&status, &existingLease, &retryAt, &inputWatermark, &finishedAt, &lastError,
	)
	if err == sql.ErrNoRows {
		result, err := conn.ExecContext(ctx, `
INSERT INTO jobs (
    kind, job_key, status, worker_id, ownership_token, started_at, finished_at,
    lease_until, retry_at, retry_remaining, last_error, input_watermark, last_success_watermark
) VALUES (?, ?, 'running', ?, ?, ?, NULL, ?, NULL, ?, NULL, 0, 0)`,
			MemoryJobKindConsolidateGlobal, MemoryConsolidationJobKey, workerID,
			ownershipToken, now, leaseUntil, memoryDefaultRetryRemaining)
		if err != nil {
			return Phase2JobClaimResult{}, err
		}
		rows, err := result.RowsAffected()
		if err != nil {
			return Phase2JobClaimResult{}, err
		}
		if err := commitMemoryImmediate(ctx, conn); err != nil {
			return Phase2JobClaimResult{}, err
		}
		committed = true
		if rows == 0 {
			return Phase2JobClaimResult{Outcome: Phase2JobSkippedRunning}, nil
		}
		return Phase2JobClaimResult{Outcome: Phase2JobClaimed, OwnershipToken: ownershipToken}, nil
	}
	if err != nil {
		return Phase2JobClaimResult{}, err
	}
	watermark := inputWatermark.Int64
	var skipped Phase2JobClaimOutcome
	switch {
	case retryAt.Valid && retryAt.Int64 > now:
		skipped = Phase2JobSkippedRetryUnavailable
	case status == "running" && existingLease.Valid && existingLease.Int64 > now:
		skipped = Phase2JobSkippedRunning
	case !lastError.Valid && finishedAt.Valid && finishedAt.Int64 > cooldownCutoff:
		skipped = Phase2JobSkippedCooldown
	}
	if skipped != "" {
		if err := commitMemoryImmediate(ctx, conn); err != nil {
			return Phase2JobClaimResult{}, err
		}
		committed = true
		return Phase2JobClaimResult{Outcome: skipped}, nil
	}
	result, err := conn.ExecContext(ctx, `
UPDATE jobs SET status = 'running', worker_id = ?, ownership_token = ?,
    started_at = ?, finished_at = NULL, lease_until = ?, retry_at = NULL, last_error = NULL
WHERE kind = ? AND job_key = ?
  AND (status != 'running' OR lease_until IS NULL OR lease_until <= ?)
  AND (retry_at IS NULL OR retry_at <= ?)
  AND (last_error IS NOT NULL OR finished_at IS NULL OR finished_at <= ?)`,
		workerID, ownershipToken, now, leaseUntil,
		MemoryJobKindConsolidateGlobal, MemoryConsolidationJobKey,
		now, now, cooldownCutoff)
	if err != nil {
		return Phase2JobClaimResult{}, err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return Phase2JobClaimResult{}, err
	}
	if err := commitMemoryImmediate(ctx, conn); err != nil {
		return Phase2JobClaimResult{}, err
	}
	committed = true
	if rows == 0 {
		return Phase2JobClaimResult{Outcome: Phase2JobSkippedRunning}, nil
	}
	return Phase2JobClaimResult{Outcome: Phase2JobClaimed, OwnershipToken: ownershipToken, InputWatermark: watermark}, nil
}

func (r *StateRuntime) HeartbeatGlobalPhase2Job(ctx context.Context, ownershipToken string, leaseSeconds int64) (bool, error) {
	if err := r.requireMemoriesRuntime(); err != nil {
		return false, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	leaseUntil := time.Now().UTC().Unix() + max(leaseSeconds, 0)
	result, err := r.memoriesDB.ExecContext(ctx, `
UPDATE jobs SET lease_until = ?
WHERE kind = ? AND job_key = ? AND status = 'running' AND ownership_token = ?`,
		leaseUntil, MemoryJobKindConsolidateGlobal, MemoryConsolidationJobKey, ownershipToken)
	if err != nil {
		return false, err
	}
	rows, err := result.RowsAffected()
	return rows > 0, err
}

func (r *StateRuntime) MarkGlobalPhase2JobSucceeded(ctx context.Context, ownershipToken string, completedWatermark int64, selected []Stage1Output) (bool, error) {
	if err := r.requireMemoriesRuntime(); err != nil {
		return false, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	tx, err := r.memoriesDB.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	result, err := tx.ExecContext(ctx, `
UPDATE jobs SET status = 'done', finished_at = ?, lease_until = NULL,
    last_error = NULL, last_success_watermark = max(COALESCE(last_success_watermark, 0), ?)
WHERE kind = ? AND job_key = ? AND status = 'running' AND ownership_token = ?`,
		time.Now().UTC().Unix(), completedWatermark,
		MemoryJobKindConsolidateGlobal, MemoryConsolidationJobKey, ownershipToken)
	if err != nil {
		return false, err
	}
	rows, err := result.RowsAffected()
	if err != nil || rows == 0 {
		return false, err
	}
	if _, err := tx.ExecContext(ctx, `
UPDATE stage1_outputs SET selected_for_phase2 = 0, selected_for_phase2_source_updated_at = NULL
WHERE selected_for_phase2 != 0 OR selected_for_phase2_source_updated_at IS NOT NULL`); err != nil {
		return false, err
	}
	for _, output := range selected {
		if _, err := tx.ExecContext(ctx, `
UPDATE stage1_outputs SET selected_for_phase2 = 1, selected_for_phase2_source_updated_at = ?
WHERE thread_id = ? AND source_updated_at = ?`,
			output.SourceUpdatedAt.Unix(), output.ThreadID, output.SourceUpdatedAt.Unix()); err != nil {
			return false, err
		}
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

func (r *StateRuntime) MarkGlobalPhase2JobFailed(ctx context.Context, ownershipToken string, failureReason string, retryDelaySeconds int64) (bool, error) {
	return r.markGlobalPhase2JobFailed(ctx, ownershipToken, failureReason, retryDelaySeconds, false)
}

func (r *StateRuntime) MarkGlobalPhase2JobFailedIfUnowned(ctx context.Context, ownershipToken string, failureReason string, retryDelaySeconds int64) (bool, error) {
	return r.markGlobalPhase2JobFailed(ctx, ownershipToken, failureReason, retryDelaySeconds, true)
}

func (r *StateRuntime) markGlobalPhase2JobFailed(ctx context.Context, ownershipToken string, failureReason string, retryDelaySeconds int64, includeUnowned bool) (bool, error) {
	if err := r.requireMemoriesRuntime(); err != nil {
		return false, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	now := time.Now().UTC().Unix()
	query := `
UPDATE jobs SET status = 'error', finished_at = ?, lease_until = NULL,
    retry_at = ?, retry_remaining = max(retry_remaining - 1, 0), last_error = ?
WHERE kind = ? AND job_key = ? AND status = 'running' AND ownership_token = ?`
	if includeUnowned {
		query = `
UPDATE jobs SET status = 'error', finished_at = ?, lease_until = NULL,
    retry_at = ?, retry_remaining = max(retry_remaining - 1, 0), last_error = ?
WHERE kind = ? AND job_key = ? AND status = 'running'
  AND (ownership_token = ? OR ownership_token IS NULL)`
	}
	result, err := r.memoriesDB.ExecContext(ctx, query,
		now, now+max(retryDelaySeconds, 0), failureReason,
		MemoryJobKindConsolidateGlobal, MemoryConsolidationJobKey, ownershipToken)
	if err != nil {
		return false, err
	}
	rows, err := result.RowsAffected()
	return rows > 0, err
}

type memoryExecer interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

func enqueueGlobalConsolidation(ctx context.Context, executor memoryExecer, inputWatermark int64) error {
	_, err := executor.ExecContext(ctx, `
INSERT INTO jobs (
    kind, job_key, status, worker_id, ownership_token, started_at, finished_at,
    lease_until, retry_at, retry_remaining, last_error, input_watermark, last_success_watermark
) VALUES (?, ?, 'pending', NULL, NULL, NULL, NULL, NULL, NULL, ?, NULL, ?, 0)
ON CONFLICT(kind, job_key) DO UPDATE SET
    status = CASE WHEN jobs.status = 'running' THEN 'running' ELSE 'pending' END,
    retry_at = CASE WHEN jobs.status = 'running' THEN jobs.retry_at ELSE NULL END,
    retry_remaining = max(jobs.retry_remaining, excluded.retry_remaining),
    input_watermark = CASE
        WHEN excluded.input_watermark > COALESCE(jobs.input_watermark, 0)
            THEN excluded.input_watermark
        ELSE COALESCE(jobs.input_watermark, 0) + 1
    END`, MemoryJobKindConsolidateGlobal, MemoryConsolidationJobKey,
		memoryDefaultRetryRemaining, inputWatermark)
	return err
}

func beginMemoryImmediate(ctx context.Context, db *sql.DB) (*sql.Conn, error) {
	conn, err := db.Conn(ctx)
	if err != nil {
		return nil, err
	}
	if _, err := conn.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		_ = conn.Close()
		return nil, err
	}
	return conn, nil
}

func commitMemoryImmediate(ctx context.Context, conn *sql.Conn) error {
	_, err := conn.ExecContext(ctx, `COMMIT`)
	return err
}
