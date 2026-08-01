package state

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"codex_go/rollout"
)

func (r *StateRuntime) MarkThreadArchived(ctx context.Context, threadID, rolloutPath string, archivedAt time.Time) error {
	if archivedAt.IsZero() {
		archivedAt = time.Now().UTC()
	}
	return r.updateThreadArchiveState(ctx, threadID, rolloutPath, true, archivedAt.Unix())
}

func (r *StateRuntime) FindRolloutPathByID(ctx context.Context, threadID string, archivedOnly *bool) (string, bool, error) {
	if r == nil || r.stateDB == nil {
		return "", false, errors.New("state runtime is nil")
	}
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return "", false, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	query := `SELECT rollout_path FROM threads WHERE id = ?`
	args := []any{threadID}
	if archivedOnly != nil {
		query += ` AND archived = ?`
		args = append(args, *archivedOnly)
	}
	var path string
	err := r.stateDB.QueryRowContext(ctx, query, args...).Scan(&path)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("find rollout path for thread %s: %w", threadID, err)
	}
	return filepath.Clean(path), true, nil
}

// ReadRepairRolloutPath updates only SQLite-owned lookup fields when a row
// exists, and rebuilds the row from JSONL only when it is absent.
func (r *StateRuntime) ReadRepairRolloutPath(ctx context.Context, threadID, rolloutPath string, archived bool) error {
	if r == nil || r.stateDB == nil {
		return errors.New("state runtime is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	threadID = strings.TrimSpace(threadID)
	sourcePath := filepath.Clean(strings.TrimSpace(rolloutPath))
	canonicalPath := filepath.Clean(strings.TrimSpace(rollout.PlainRolloutPath(sourcePath)))
	if threadID == "" || sourcePath == "." || sourcePath == "" || canonicalPath == "." || canonicalPath == "" {
		return errors.New("thread id and rollout path are required")
	}
	result, err := r.stateDB.ExecContext(ctx, `
UPDATE threads
SET rollout_path = ?,
    archived = ?,
    archived_at = CASE
        WHEN ? AND archived_at IS NULL THEN updated_at
        WHEN NOT ? THEN NULL
        ELSE archived_at
    END
WHERE id = ?`, canonicalPath, archived, archived, archived, threadID)
	if err != nil {
		return fmt.Errorf("read-repair thread %s: %w", threadID, err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows > 0 {
		return nil
	}
	return r.ReconcileRollout(ctx, sourcePath, archived)
}

func (r *StateRuntime) MarkThreadUnarchived(ctx context.Context, threadID, rolloutPath string) error {
	return r.updateThreadArchiveState(ctx, threadID, rolloutPath, false, nil)
}

func (r *StateRuntime) updateThreadArchiveState(ctx context.Context, threadID, rolloutPath string, archived bool, archivedAt any) error {
	if r == nil || r.stateDB == nil {
		return errors.New("state runtime is nil")
	}
	threadID = strings.TrimSpace(threadID)
	rolloutPath = strings.TrimSpace(rolloutPath)
	if threadID == "" || rolloutPath == "" {
		return errors.New("thread id and rollout path are required")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	updatedAt := time.Now().UTC()
	if info, err := os.Stat(rolloutPath); err == nil {
		updatedAt = info.ModTime().UTC()
	}
	updatedMillis := r.allocateThreadTimestamp(&r.threadUpdatedAt, updatedAt.UnixMilli())
	_, err := r.stateDB.ExecContext(ctx, `
UPDATE threads
SET rollout_path = ?, archived = ?, archived_at = ?, updated_at = ?, updated_at_ms = ?
WHERE id = ?`, rolloutPath, archived, archivedAt, updatedAt.Unix(), updatedMillis, threadID)
	if err != nil {
		return fmt.Errorf("update thread archive state %s: %w", threadID, err)
	}
	return nil
}

// DeleteThreadHistory removes the rebuildable history projection without
// creating the lazy database solely for deletion.
func (r *StateRuntime) DeleteThreadHistory(ctx context.Context, threadID string) error {
	if r == nil {
		return errors.New("state runtime is nil")
	}
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return errors.New("thread id is required")
	}
	if _, err := os.Stat(r.sqlite.ThreadHistoryDBPath()); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return fmt.Errorf("stat thread history database: %w", err)
	}
	db, err := r.ThreadHistoryDB(ctx)
	if err != nil {
		return err
	}
	return deleteThreadHistoryRows(ctx, db, threadID)
}

func deleteThreadHistoryRows(ctx context.Context, db *sql.DB, threadID string) (err error) {
	if ctx == nil {
		ctx = context.Background()
	}
	conn, err := db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		return fmt.Errorf("begin thread history delete: %w", err)
	}
	defer func() {
		if err != nil {
			_, _ = conn.ExecContext(context.Background(), `ROLLBACK`)
		}
	}()
	for _, table := range []string{"thread_items", "thread_turns", "thread_history_projection_state"} {
		if _, err = conn.ExecContext(ctx, `DELETE FROM `+table+` WHERE thread_id = ?`, threadID); err != nil {
			return fmt.Errorf("delete %s for thread %s: %w", table, threadID, err)
		}
	}
	if _, err = conn.ExecContext(ctx, `COMMIT`); err != nil {
		return fmt.Errorf("commit thread history delete: %w", err)
	}
	return nil
}

// DeleteThreadsStrict mirrors Rust cleanup ordering: auxiliary databases are
// cleaned first, then spawn edges and primary thread rows are deleted last.
func (r *StateRuntime) DeleteThreadsStrict(ctx context.Context, threadIDs []string) (int64, error) {
	if r == nil || r.stateDB == nil {
		return 0, errors.New("state runtime is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	ids := compactThreadIDs(threadIDs)
	for _, threadID := range ids {
		if _, err := r.logsDB.ExecContext(ctx, `DELETE FROM logs WHERE thread_id = ?`, threadID); err != nil {
			return 0, fmt.Errorf("delete logs for thread %s: %w", threadID, err)
		}
		if err := r.deleteThreadMemory(ctx, threadID); err != nil {
			return 0, err
		}
		if _, err := r.goalsDB.ExecContext(ctx, `DELETE FROM thread_goals WHERE thread_id = ?`, threadID); err != nil {
			return 0, fmt.Errorf("delete goal for thread %s: %w", threadID, err)
		}
	}
	if len(ids) == 0 {
		return 0, nil
	}
	tx, err := r.stateDB.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin thread state delete: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	for _, threadID := range ids {
		if _, err := tx.ExecContext(ctx, `DELETE FROM thread_dynamic_tools WHERE thread_id = ?`, threadID); err != nil {
			return 0, fmt.Errorf("delete dynamic tools for thread %s: %w", threadID, err)
		}
	}
	for _, threadID := range ids {
		if _, err := tx.ExecContext(ctx, `DELETE FROM thread_spawn_edges WHERE parent_thread_id = ? OR child_thread_id = ?`, threadID, threadID); err != nil {
			return 0, fmt.Errorf("delete spawn edges for thread %s: %w", threadID, err)
		}
	}
	var deleted int64
	for _, threadID := range ids {
		result, err := tx.ExecContext(ctx, `DELETE FROM threads WHERE id = ?`, threadID)
		if err != nil {
			return 0, fmt.Errorf("delete thread %s: %w", threadID, err)
		}
		rows, err := result.RowsAffected()
		if err != nil {
			return 0, err
		}
		deleted += rows
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit thread state delete: %w", err)
	}
	return deleted, nil
}

func (r *StateRuntime) deleteThreadMemory(ctx context.Context, threadID string) error {
	tx, err := r.memoriesDB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin memory delete for thread %s: %w", threadID, err)
	}
	defer func() { _ = tx.Rollback() }()
	var selected int
	err = tx.QueryRowContext(ctx, `SELECT selected_for_phase2 FROM stage1_outputs WHERE thread_id = ?`, threadID).Scan(&selected)
	if err != nil && err != sql.ErrNoRows {
		return fmt.Errorf("read memory output for thread %s: %w", threadID, err)
	}
	result, err := tx.ExecContext(ctx, `DELETE FROM stage1_outputs WHERE thread_id = ?`, threadID)
	if err != nil {
		return fmt.Errorf("delete memory output for thread %s: %w", threadID, err)
	}
	deleted, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM jobs WHERE kind = 'memory_stage1' AND job_key = ?`, threadID); err != nil {
		return fmt.Errorf("delete memory job for thread %s: %w", threadID, err)
	}
	if deleted > 0 && selected != 0 {
		now := time.Now().Unix()
		if _, err := tx.ExecContext(ctx, `
INSERT INTO jobs (
    kind, job_key, status, worker_id, ownership_token, started_at, finished_at,
    lease_until, retry_at, retry_remaining, last_error, input_watermark, last_success_watermark
) VALUES ('memory_consolidate_global', 'global', 'pending', NULL, NULL, NULL, NULL, NULL, NULL, 3, NULL, ?, 0)
ON CONFLICT(kind, job_key) DO UPDATE SET
    status = CASE WHEN jobs.status = 'running' THEN 'running' ELSE 'pending' END,
    retry_at = CASE WHEN jobs.status = 'running' THEN jobs.retry_at ELSE NULL END,
    retry_remaining = max(jobs.retry_remaining, excluded.retry_remaining),
    input_watermark = CASE
        WHEN excluded.input_watermark > COALESCE(jobs.input_watermark, 0)
            THEN excluded.input_watermark
        ELSE COALESCE(jobs.input_watermark, 0) + 1
    END`, now); err != nil {
			return fmt.Errorf("enqueue memory consolidation after deleting thread %s: %w", threadID, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit memory delete for thread %s: %w", threadID, err)
	}
	return nil
}

func compactThreadIDs(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}
