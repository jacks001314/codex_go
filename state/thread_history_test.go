package state

import (
	"context"
	"crypto/sha512"
	"database/sql"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

func TestOpenThreadHistoryDBCreatesRustCompatibleSchema(t *testing.T) {
	home := t.TempDir()
	db, err := OpenThreadHistoryDB(context.Background(), home)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := os.Stat(filepath.Join(home, "thread_history_1.sqlite")); err != nil {
		t.Fatal(err)
	}
	for table, columns := range map[string][]string{
		"thread_turns":                    {"thread_id", "turn_id", "rollout_ordinal", "rollout_byte_offset", "rollout_end_ordinal", "rollout_end_byte_offset", "status", "error_json", "started_at", "completed_at", "duration_ms", "first_user_item_id", "final_agent_item_id"},
		"thread_items":                    {"thread_id", "turn_id", "item_id", "rollout_ordinal", "updated_at_ordinal", "created_at_ms", "item_type", "item_json"},
		"thread_history_projection_state": {"thread_id", "next_rollout_byte_offset", "next_rollout_ordinal"},
	} {
		assertSQLiteColumns(t, db, table, columns)
	}
	for _, index := range []string{
		"idx_thread_turns_page",
		"idx_thread_items_page",
		"idx_thread_items_by_turn_page",
		"idx_thread_items_user_messages",
		"idx_thread_items_updated_page",
		"idx_thread_items_by_turn_updated_page",
	} {
		var count int
		if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'index' AND name = ?`, index).Scan(&count); err != nil || count != 1 {
			t.Fatalf("index %s count/error = %d/%v", index, count, err)
		}
	}
}

func TestThreadHistoryMigrationsUseSQLxChecksums(t *testing.T) {
	home := t.TempDir()
	db, err := OpenThreadHistoryDB(context.Background(), home)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	wantChecksums := map[int64]string{
		1: "2ad9a4ab17df511fb73f03337133de94a172dc0a1f5729b5ef61fe106db03d1a43bdb5978b0cb7d7bc74071d3ca34a92",
		2: "ae61dbc1a6422530aa93914dbcac363a0238b86ae1a9d77eb041c75330e1283a0fcf93a043deeefcd6fd2c4037694bc4",
		3: "a51da2a11df7760d8f8cab221e82b36ce8c0a30f16d44f3602e7f0fbc622ca72f1c3e3ecf22a194e12c2222bdcb51afe",
		4: "65f33b171bd3aafe9d24728b2ca6a5c720a3ab54aed472998f8acf7489f0d35b2dfd12258a2d17be993bf17098b5e50c",
		// Upstream ce0323f2f2 (#54776): the realtime-item projection, turn-end
		// paging index, item lifecycle timestamps and turn-root columns.
		5: "2fdad8e361dc1f828afacf10bd2eb1f511b02deef7fa234a9c412d0211de126d1cfad54b175c9c3a9731548f9084b437",
		6: "02ed82d375adb58b5c0c09e0154f3331d507cea7af446d12324e5653cdba9a91f84afd38cf2a29f33030614689deb9de",
		7: "9334adb4cda792116a47b69a19e3840ae3806b08773ef1794043fe0aa72891360acb1a0a8431084d53c568482c5fdd20",
		8: "94635cd58535adb1679f4ad07a9d14231b69004405fa88a22db1b6a7e5feac8b6b66ad97ea16fce464b99bff5b3548d3",
	}
	migrations, err := loadRuntimeMigrations(RuntimeDBThreadHistory)
	if err != nil {
		t.Fatal(err)
	}
	if len(migrations) != 8 {
		t.Fatalf("thread history migration count = %d, want 8", len(migrations))
	}
	for _, migration := range migrations {
		var description string
		var checksum []byte
		var success bool
		if err := db.QueryRow(`SELECT description, checksum, success FROM _sqlx_migrations WHERE version = ?`, migration.version).Scan(&description, &checksum, &success); err != nil {
			t.Fatal(err)
		}
		want := sha512.Sum384([]byte(migration.sql))
		if description != migration.description || !success || string(checksum) != string(want[:]) || hex.EncodeToString(want[:]) != wantChecksums[migration.version] {
			t.Fatalf("migration %d metadata does not match SQLx", migration.version)
		}
	}
	if err := migrateThreadHistory(context.Background(), db); err != nil {
		t.Fatalf("idempotent migrate: %v", err)
	}
	second, err := OpenThreadHistoryDB(context.Background(), home)
	if err != nil {
		t.Fatalf("reopen migrated database: %v", err)
	}
	_ = second.Close()
}

func assertSQLiteColumns(t *testing.T, db *sql.DB, table string, expected []string) {
	t.Helper()
	rows, err := db.Query(`PRAGMA table_info(` + table + `)`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	got := map[string]bool{}
	for rows.Next() {
		var cid int
		var name, columnType string
		var notNull, primaryKey int
		var defaultValue any
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			t.Fatal(err)
		}
		got[name] = true
	}
	for _, column := range expected {
		if !got[column] {
			t.Fatalf("table %s missing column %s; got %v", table, column, got)
		}
	}
}
