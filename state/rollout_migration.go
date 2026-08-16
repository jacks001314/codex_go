package state

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// RolloutMigrationState mirrors Rust codex_state::RolloutMigrationState.
type RolloutMigrationState struct {
	LastCheckedThread *RolloutMigrationCursor
}

// RolloutMigrationCursor mirrors Rust codex_state::RolloutMigrationCursor.
type RolloutMigrationCursor struct {
	ThreadCreatedAt int64
	ThreadID        string
}

// RolloutMigrationSkippedRollout mirrors Rust
// codex_state::RolloutMigrationSkippedRollout.
type RolloutMigrationSkippedRollout struct {
	RolloutPath       string
	RolloutSizeBytes  int64
	RolloutModifiedNs int64
	SkipReason        string
}

// GetRolloutMigrationState reads one migration's checked frontier.
func GetRolloutMigrationState(ctx context.Context, db *sql.DB, migrationID string) (*RolloutMigrationState, error) {
	if db == nil {
		return nil, errors.New("state database is nil")
	}
	var createdAt sql.NullInt64
	var threadID sql.NullString
	err := db.QueryRowContext(ctx, `
SELECT last_checked_thread_created_at, last_checked_thread_id
FROM rollout_migration_state
WHERE migration_id = ?`, migrationID).Scan(&createdAt, &threadID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read rollout migration state: %w", err)
	}
	state := &RolloutMigrationState{}
	if createdAt.Valid && threadID.Valid && strings.TrimSpace(threadID.String) != "" {
		state.LastCheckedThread = &RolloutMigrationCursor{
			ThreadCreatedAt: createdAt.Int64,
			ThreadID:        strings.TrimSpace(threadID.String),
		}
	}
	return state, nil
}

// AdvanceRolloutMigrationState advances one migration's checked frontier
// without letting concurrent startup checks move it backward.
func AdvanceRolloutMigrationState(ctx context.Context, db *sql.DB, migrationID string, lastCheckedThread *RolloutMigrationCursor) error {
	if db == nil {
		return errors.New("state database is nil")
	}
	now := time.Now().Unix()
	var createdAt any
	var threadID any
	if lastCheckedThread != nil {
		createdAt = lastCheckedThread.ThreadCreatedAt
		threadID = lastCheckedThread.ThreadID
	}
	_, err := db.ExecContext(ctx, `
INSERT INTO rollout_migration_state (
    migration_id,
    last_checked_thread_created_at,
    last_checked_thread_id,
    updated_at
)
VALUES (?, ?, ?, ?)
ON CONFLICT(migration_id) DO UPDATE SET
    last_checked_thread_created_at = excluded.last_checked_thread_created_at,
    last_checked_thread_id = excluded.last_checked_thread_id,
    updated_at = excluded.updated_at
WHERE excluded.last_checked_thread_created_at IS NOT NULL
  AND (
    rollout_migration_state.last_checked_thread_created_at IS NULL
    OR excluded.last_checked_thread_created_at
        > rollout_migration_state.last_checked_thread_created_at
    OR (
        excluded.last_checked_thread_created_at
            = rollout_migration_state.last_checked_thread_created_at
        AND excluded.last_checked_thread_id > rollout_migration_state.last_checked_thread_id
    )
  )`, migrationID, createdAt, threadID, now)
	if err != nil {
		return fmt.Errorf("advance rollout migration state: %w", err)
	}
	return nil
}

// RemoveRolloutMigrationSkip removes a fingerprinted skip once the rollout is
// no longer skipped (migrated or already paginated).
func RemoveRolloutMigrationSkip(ctx context.Context, db *sql.DB, migrationID string, rolloutPath string) error {
	if db == nil {
		return errors.New("state database is nil")
	}
	if _, err := db.ExecContext(ctx, `
DELETE FROM rollout_migration_skipped_rollouts
WHERE migration_id = ? AND rollout_path = ?`, migrationID, rolloutPath); err != nil {
		return fmt.Errorf("remove rollout migration skip: %w", err)
	}
	return nil
}

// ListRolloutMigrationSkippedRollouts returns fingerprinted rollouts a
// migration could not process.
func ListRolloutMigrationSkippedRollouts(ctx context.Context, db *sql.DB, migrationID string) ([]RolloutMigrationSkippedRollout, error) {
	if db == nil {
		return nil, errors.New("state database is nil")
	}
	rows, err := db.QueryContext(ctx, `
SELECT rollout_path, rollout_size_bytes, rollout_modified_at_ns, skip_reason
FROM rollout_migration_skipped_rollouts
WHERE migration_id = ?`, migrationID)
	if err != nil {
		return nil, fmt.Errorf("list rollout migration skipped rollouts: %w", err)
	}
	defer rows.Close()
	var out []RolloutMigrationSkippedRollout
	for rows.Next() {
		var skipped RolloutMigrationSkippedRollout
		if err := rows.Scan(&skipped.RolloutPath, &skipped.RolloutSizeBytes, &skipped.RolloutModifiedNs, &skipped.SkipReason); err != nil {
			return nil, fmt.Errorf("scan rollout migration skipped rollout: %w", err)
		}
		out = append(out, skipped)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate rollout migration skipped rollouts: %w", err)
	}
	return out, nil
}

// RecordRolloutMigrationSkip fingerprints a rollout the migration skipped.
func RecordRolloutMigrationSkip(ctx context.Context, db *sql.DB, migrationID string, skipped RolloutMigrationSkippedRollout) error {
	if db == nil {
		return errors.New("state database is nil")
	}
	now := time.Now().Unix()
	_, err := db.ExecContext(ctx, `
INSERT INTO rollout_migration_skipped_rollouts (
    migration_id,
    rollout_path,
    rollout_size_bytes,
    rollout_modified_at_ns,
    skip_reason,
    skipped_at
)
VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT(migration_id, rollout_path) DO UPDATE SET
    rollout_size_bytes = excluded.rollout_size_bytes,
    rollout_modified_at_ns = excluded.rollout_modified_at_ns,
    skip_reason = excluded.skip_reason,
    skipped_at = excluded.skipped_at`, migrationID, skipped.RolloutPath, skipped.RolloutSizeBytes, skipped.RolloutModifiedNs, skipped.SkipReason, now)
	if err != nil {
		return fmt.Errorf("record rollout migration skip: %w", err)
	}
	return nil
}
