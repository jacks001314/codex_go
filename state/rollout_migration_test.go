package state

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func openRolloutMigrationTestStateDB(t *testing.T) *sql.DB {
	t.Helper()
	config, err := NewSqliteConfig(t.TempDir())
	if err != nil {
		t.Fatalf("NewSqliteConfig: %v", err)
	}
	db, err := config.OpenStateDB(context.Background())
	if err != nil {
		t.Fatalf("OpenStateDB: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestReadThreadRolloutPathsReadsIndexWithoutCreatingDatabaseLikeRust(t *testing.T) {
	// Mirrors Rust read_thread_rollout_paths (#57483): the index is read from an
	// existing database only, and a missing database reports an error instead of
	// being created or migrated.
	home := t.TempDir()
	ctx := context.Background()
	if _, err := ReadThreadRolloutPaths(ctx, home); err == nil {
		t.Fatal("ReadThreadRolloutPaths on a missing state database returned no error")
	}
	config, err := NewSqliteConfig(home)
	if err != nil {
		t.Fatalf("NewSqliteConfig: %v", err)
	}
	if _, err := os.Stat(config.StateDBPath()); !os.IsNotExist(err) {
		t.Fatalf("missing state database was created (err=%v)", err)
	}
	db, err := config.OpenStateDB(ctx)
	if err != nil {
		t.Fatalf("OpenStateDB: %v", err)
	}
	for _, row := range []struct{ id, path string }{
		{id: "thread-a", path: filepath.Join("sessions", "rollout-2025-01-01T00-00-00-thread-a.jsonl")},
		{id: "thread-b", path: filepath.Join("sessions", "rollout-2025-01-01T00-00-00-thread-b.jsonl.zst")},
	} {
		if _, err := db.ExecContext(ctx, `INSERT INTO threads
			(id, rollout_path, created_at, updated_at, created_at_ms, updated_at_ms, recency_at_ms, source, model_provider, cwd, title, sandbox_policy, approval_mode)
			VALUES (?, ?, 1, 1, 1, 1, 1, 'cli', 'openai', '/workspace', 'thread', '', '')`, row.id, row.path); err != nil {
			t.Fatalf("insert thread %s: %v", row.id, err)
		}
	}
	_ = db.Close()

	paths, err := ReadThreadRolloutPaths(ctx, home)
	if err != nil {
		t.Fatalf("ReadThreadRolloutPaths: %v", err)
	}
	if len(paths) != 2 {
		t.Fatalf("paths = %#v, want two rows", paths)
	}
	byID := map[string]string{}
	for _, path := range paths {
		byID[path.ThreadID] = path.RolloutPath
	}
	if byID["thread-a"] == "" || !strings.HasSuffix(byID["thread-b"], ".jsonl.zst") {
		t.Fatalf("indexed paths = %#v", paths)
	}
}

func TestRolloutMigrationStateCursorAdvancesMonotonicallyLikeRust(t *testing.T) {
	// Mirrors Rust advance_rollout_migration_state: the checked frontier only
	// moves forward; an older cursor is a no-op.
	db := openRolloutMigrationTestStateDB(t)
	ctx := context.Background()
	const migrationID = "legacy_to_paginated_v1"

	if state, err := GetRolloutMigrationState(ctx, db, migrationID); err != nil || state != nil {
		t.Fatalf("initial state = %#v err=%v, want nil", state, err)
	}

	if err := AdvanceRolloutMigrationState(ctx, db, migrationID, &RolloutMigrationCursor{ThreadCreatedAt: 100, ThreadID: "thread-a"}); err != nil {
		t.Fatalf("advance: %v", err)
	}
	state, err := GetRolloutMigrationState(ctx, db, migrationID)
	if err != nil || state == nil || state.LastCheckedThread == nil {
		t.Fatalf("state after advance = %#v err=%v", state, err)
	}
	if state.LastCheckedThread.ThreadCreatedAt != 100 || state.LastCheckedThread.ThreadID != "thread-a" {
		t.Fatalf("cursor = %#v, want 100/thread-a", state.LastCheckedThread)
	}

	// Older cursor must not move the frontier backward.
	if err := AdvanceRolloutMigrationState(ctx, db, migrationID, &RolloutMigrationCursor{ThreadCreatedAt: 50, ThreadID: "thread-old"}); err != nil {
		t.Fatalf("advance older: %v", err)
	}
	state, _ = GetRolloutMigrationState(ctx, db, migrationID)
	if state.LastCheckedThread.ThreadCreatedAt != 100 || state.LastCheckedThread.ThreadID != "thread-a" {
		t.Fatalf("frontier moved backward: %#v", state.LastCheckedThread)
	}

	// Newer cursor advances.
	if err := AdvanceRolloutMigrationState(ctx, db, migrationID, &RolloutMigrationCursor{ThreadCreatedAt: 200, ThreadID: "thread-b"}); err != nil {
		t.Fatalf("advance newer: %v", err)
	}
	state, _ = GetRolloutMigrationState(ctx, db, migrationID)
	if state.LastCheckedThread.ThreadCreatedAt != 200 || state.LastCheckedThread.ThreadID != "thread-b" {
		t.Fatalf("frontier did not advance: %#v", state.LastCheckedThread)
	}
}

func TestRolloutMigrationSkippedRolloutsRoundTrip(t *testing.T) {
	db := openRolloutMigrationTestStateDB(t)
	ctx := context.Background()
	const migrationID = "legacy_to_paginated_v1"

	skipped := []RolloutMigrationSkippedRollout{
		{RolloutPath: filepath.Join("sessions", "a.jsonl"), RolloutSizeBytes: 10, RolloutModifiedNs: 1000, SkipReason: "empty"},
		{RolloutPath: filepath.Join("sessions", "b.jsonl"), RolloutSizeBytes: 20, RolloutModifiedNs: 2000, SkipReason: "malformed_session_meta"},
	}
	for _, entry := range skipped {
		if err := RecordRolloutMigrationSkip(ctx, db, migrationID, entry); err != nil {
			t.Fatalf("record skip: %v", err)
		}
	}
	got, err := ListRolloutMigrationSkippedRollouts(ctx, db, migrationID)
	if err != nil {
		t.Fatalf("list skipped: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("skipped count = %d, want 2", len(got))
	}
	byPath := map[string]RolloutMigrationSkippedRollout{}
	for _, entry := range got {
		byPath[entry.RolloutPath] = entry
	}
	for _, want := range skipped {
		entry, ok := byPath[want.RolloutPath]
		if !ok {
			t.Fatalf("missing skipped rollout %s", want.RolloutPath)
		}
		if entry.RolloutSizeBytes != want.RolloutSizeBytes || entry.RolloutModifiedNs != want.RolloutModifiedNs || entry.SkipReason != want.SkipReason {
			t.Fatalf("skipped %s = %#v, want %#v", want.RolloutPath, entry, want)
		}
	}

	// Upsert on the same path updates the fingerprint.
	updated := RolloutMigrationSkippedRollout{RolloutPath: filepath.Join("sessions", "a.jsonl"), RolloutSizeBytes: 99, RolloutModifiedNs: 999, SkipReason: "empty"}
	if err := RecordRolloutMigrationSkip(ctx, db, migrationID, updated); err != nil {
		t.Fatalf("record skip upsert: %v", err)
	}
	got, _ = ListRolloutMigrationSkippedRollouts(ctx, db, migrationID)
	if len(got) != 2 {
		t.Fatalf("skipped count after upsert = %d, want 2", len(got))
	}
}
