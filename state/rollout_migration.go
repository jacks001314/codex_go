package state

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
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

// ThreadRolloutPath is one indexed thread's rollout location (Rust #57483).
type ThreadRolloutPath struct {
	ThreadID    string
	RolloutPath string
}

// ReadThreadRolloutPaths reads the thread IDs and rollout paths an existing
// state database has indexed, without creating or migrating it (Rust #57483).
// Selected-thread rollout migration uses the index to skip rollout headers that
// provably belong to unrelated threads. Callers treat an error as "index
// unavailable" and fall back to inspecting every header.
func ReadThreadRolloutPaths(ctx context.Context, sqliteHome string) ([]ThreadRolloutPath, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	config, err := NewSqliteConfig(sqliteHome)
	if err != nil {
		return nil, err
	}
	path := config.StateDBPath()
	if _, err := os.Stat(path); err != nil {
		return nil, fmt.Errorf("state database is unavailable: %w", err)
	}
	// Migration can scan rollout headers immediately if the index is locked, so
	// the read waits for no lock (Rust passes a zero busy timeout).
	db, err := config.OpenReadOnly(ctx, path)
	if err != nil {
		return nil, fmt.Errorf("open state database read-only: %w", err)
	}
	defer func() { _ = db.Close() }()
	rows, err := db.QueryContext(ctx, `SELECT id, rollout_path FROM threads`)
	if err != nil {
		return nil, fmt.Errorf("read indexed rollout paths: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var paths []ThreadRolloutPath
	for rows.Next() {
		var threadID string
		var rolloutPath sql.NullString
		if err := rows.Scan(&threadID, &rolloutPath); err != nil {
			return nil, fmt.Errorf("scan indexed rollout path: %w", err)
		}
		threadID = strings.TrimSpace(threadID)
		if threadID == "" || !rolloutPath.Valid || strings.TrimSpace(rolloutPath.String) == "" {
			continue
		}
		paths = append(paths, ThreadRolloutPath{
			ThreadID:    threadID,
			RolloutPath: strings.TrimSpace(rolloutPath.String),
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read indexed rollout paths: %w", err)
	}
	return paths, nil
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
	_, err := ExecWrite(ctx, db, `
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
	if _, err := ExecWrite(ctx, db, `
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
	_, err := ExecWrite(ctx, db, `
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
