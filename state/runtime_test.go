package state

import (
	"bytes"
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStateRuntimeOwnsRustDatabaseLifecycle(t *testing.T) {
	ctx := context.Background()
	config, err := NewSqliteConfig(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := InitStateRuntime(ctx, config, "openai")
	if err != nil {
		t.Fatalf("InitStateRuntime() error = %v", err)
	}
	defer runtime.Close()

	if runtime.DefaultProvider() != "openai" || runtime.SQLite() != config {
		t.Fatalf("runtime configuration mismatch: %#v", runtime)
	}
	for _, path := range []string{config.StateDBPath(), config.LogsDBPath(), config.GoalsDBPath(), config.MemoriesDBPath()} {
		if info, err := os.Stat(path); err != nil || !info.Mode().IsRegular() {
			t.Fatalf("runtime database %s missing: info=%#v err=%v", path, info, err)
		}
	}
	if _, err := os.Stat(config.ThreadHistoryDBPath()); !os.IsNotExist(err) {
		t.Fatalf("thread history must remain lazy, stat err=%v", err)
	}

	for _, test := range []struct {
		name string
		db   *sql.DB
		want int
	}{
		{name: "state", db: runtime.StateDB(), want: 49},
		{name: "logs", db: runtime.LogsDB(), want: 2},
		{name: "goals", db: runtime.GoalsDB(), want: 2},
		{name: "memories", db: runtime.MemoriesDB(), want: 1},
	} {
		var count int
		if err := test.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM _sqlx_migrations`).Scan(&count); err != nil || count != test.want {
			t.Fatalf("%s migration count = %d, %v; want %d", test.name, count, err, test.want)
		}
	}
	var backfillStatus string
	if err := runtime.StateDB().QueryRowContext(ctx, `SELECT status FROM backfill_state WHERE id = 1`).Scan(&backfillStatus); err != nil || backfillStatus != "pending" {
		t.Fatalf("backfill state = %q, %v", backfillStatus, err)
	}

	history, err := config.OpenThreadHistoryDB(ctx)
	if err != nil {
		t.Fatalf("OpenThreadHistoryDB() error = %v", err)
	}
	if err := history.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(config.ThreadHistoryDBPath()); err != nil {
		t.Fatalf("lazy thread history was not created: %v", err)
	}
}

func TestWritableSQLitePragmasApplyToEveryPoolConnection(t *testing.T) {
	ctx := context.Background()
	config, err := NewSqliteConfig(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	db, err := config.OpenReadWrite(ctx, config.StateDBPath())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if got := db.Stats().MaxOpenConnections; got != SQLiteMaxOpenConnections {
		t.Fatalf("MaxOpenConnections = %d, want %d", got, SQLiteMaxOpenConnections)
	}

	connections := make([]*sql.Conn, 0, SQLiteMaxOpenConnections)
	defer func() {
		for _, conn := range connections {
			_ = conn.Close()
		}
	}()
	for i := 0; i < SQLiteMaxOpenConnections; i++ {
		conn, err := db.Conn(ctx)
		if err != nil {
			t.Fatal(err)
		}
		connections = append(connections, conn)
	}
	for i, conn := range connections {
		var busyTimeout, synchronous, autoVacuum, foreignKeys int
		var journalMode string
		if err := conn.QueryRowContext(ctx, `PRAGMA busy_timeout`).Scan(&busyTimeout); err != nil {
			t.Fatal(err)
		}
		if err := conn.QueryRowContext(ctx, `PRAGMA journal_mode`).Scan(&journalMode); err != nil {
			t.Fatal(err)
		}
		if err := conn.QueryRowContext(ctx, `PRAGMA synchronous`).Scan(&synchronous); err != nil {
			t.Fatal(err)
		}
		if err := conn.QueryRowContext(ctx, `PRAGMA auto_vacuum`).Scan(&autoVacuum); err != nil {
			t.Fatal(err)
		}
		if err := conn.QueryRowContext(ctx, `PRAGMA foreign_keys`).Scan(&foreignKeys); err != nil {
			t.Fatal(err)
		}
		if busyTimeout != 5000 || strings.ToLower(journalMode) != "wal" || synchronous != 1 || autoVacuum != 2 || foreignKeys != 1 {
			t.Fatalf("connection %d pragmas = busy:%d journal:%q sync:%d vacuum:%d foreign_keys:%d", i, busyTimeout, journalMode, synchronous, autoVacuum, foreignKeys)
		}
	}
}

func TestStateRuntimeClosePreventsLazyHistoryReopen(t *testing.T) {
	config, err := NewSqliteConfig(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := InitStateRuntime(context.Background(), config, "openai")
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.ThreadHistoryDB(context.Background()); err == nil || !strings.Contains(err.Error(), "closed") {
		t.Fatalf("ThreadHistoryDB() after Close error = %v", err)
	}
	if _, err := os.Stat(config.ThreadHistoryDBPath()); !os.IsNotExist(err) {
		t.Fatalf("closed runtime recreated lazy history database: %v", err)
	}
}

func TestRuntimeMigrationsValidateKnownAndIgnoreNewerVersions(t *testing.T) {
	ctx := context.Background()
	config, err := NewSqliteConfig(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	db, err := config.OpenStateDB(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `
INSERT INTO _sqlx_migrations (version, description, success, checksum, execution_time)
VALUES (999, 'future migration', TRUE, X'010203', 1)`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = config.OpenStateDB(ctx)
	if err != nil {
		t.Fatalf("newer migration should be ignored: %v", err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE _sqlx_migrations SET checksum = X'00' WHERE version = 1`); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	_, err = config.OpenStateDB(ctx)
	if err == nil || !strings.Contains(err.Error(), "migration 1 checksum mismatch") {
		t.Fatalf("known checksum mismatch error = %v", err)
	}
}

func TestRuntimeMigrationsAcceptRustStateChecksums(t *testing.T) {
	ctx := context.Background()
	config, err := NewSqliteConfig(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	db, err := config.OpenStateDB(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE _sqlx_migrations SET checksum = ? WHERE version = 1`, rustMigrationOneChecksums[RuntimeDBState]); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE _sqlx_migrations SET checksum = X'01' WHERE version = 2`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if db, err = config.OpenStateDB(ctx); err != nil {
		t.Fatalf("Rust state checksums should be accepted: %v", err)
	}
	_ = db.Close()
}

func TestRuntimeMigrationsRejectRustFingerprintWithWrongDescription(t *testing.T) {
	ctx := context.Background()
	config, err := NewSqliteConfig(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	db, err := config.OpenStateDB(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE _sqlx_migrations SET checksum = ?, description = 'unexpected' WHERE version = 1`, rustMigrationOneChecksums[RuntimeDBState]); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	_, err = config.OpenStateDB(ctx)
	if err == nil || !strings.Contains(err.Error(), "migration 1 description mismatch") {
		t.Fatalf("Rust fingerprint with wrong description error = %v", err)
	}
}

func TestRuntimeMigrationsRepairLegacyRecencyVersion(t *testing.T) {
	ctx := context.Background()
	config, err := NewSqliteConfig(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	db, err := config.OpenReadWrite(ctx, config.StateDBPath())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.ExecContext(ctx, `
CREATE TABLE _sqlx_migrations (
    version BIGINT PRIMARY KEY,
    description TEXT NOT NULL,
    installed_on TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    success BOOLEAN NOT NULL,
    checksum BLOB NOT NULL,
    execution_time BIGINT NOT NULL
)`); err != nil {
		t.Fatal(err)
	}
	migrations, err := loadRuntimeMigrations(RuntimeDBState)
	if err != nil {
		t.Fatal(err)
	}
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var recency sqliteMigration
	for _, migration := range migrations {
		if migration.version <= 37 {
			if err := applyRuntimeMigration(ctx, conn, migration); err != nil {
				_ = conn.Close()
				t.Fatalf("apply migration %d: %v", migration.version, err)
			}
		}
		if migration.version == 39 {
			recency = migration
		}
	}
	legacyRecency := recency
	legacyRecency.version = 38
	if err := applyRuntimeMigration(ctx, conn, legacyRecency); err != nil {
		_ = conn.Close()
		t.Fatal(err)
	}
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
	if err := migrateRuntimeDB(ctx, db, RuntimeDBState); err != nil {
		t.Fatalf("migrate after legacy repair: %v", err)
	}
	for _, migration := range migrations {
		if migration.version != 38 && migration.version != 39 {
			continue
		}
		var checksum []byte
		if err := db.QueryRowContext(ctx, `SELECT checksum FROM _sqlx_migrations WHERE version = ?`, migration.version).Scan(&checksum); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(checksum, migration.checksum[:]) {
			t.Fatalf("migration %d checksum was not repaired", migration.version)
		}
	}
}

func TestRuntimeMigrationInventoryMatchesFrozenRustCounts(t *testing.T) {
	want := map[RuntimeDBKind]int{
		RuntimeDBState:         49,
		RuntimeDBLogs:          2,
		RuntimeDBGoals:         2,
		RuntimeDBMemories:      1,
		RuntimeDBThreadHistory: 4,
	}
	for kind, count := range want {
		migrations, err := loadRuntimeMigrations(kind)
		if err != nil {
			t.Fatal(err)
		}
		if len(migrations) != count {
			t.Fatalf("%s migration count = %d, want %d", kind, len(migrations), count)
		}
		for i, migration := range migrations {
			if migration.version != int64(i+1) {
				t.Fatalf("%s migration %d has version %d", kind, i, migration.version)
			}
			if strings.Contains(migration.sql, "\r") {
				t.Fatalf("%s migration %d contains CR; SQLx checksums must use canonical LF", kind, migration.version)
			}
		}
	}
}

func TestRuntimeCorruptionErrorIdentifiesOnlyFailedDatabase(t *testing.T) {
	ctx := context.Background()
	config, err := NewSqliteConfig(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	stateDB, err := config.OpenStateDB(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_ = stateDB.Close()
	if err := os.WriteFile(config.LogsDBPath(), []byte("not sqlite"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = InitStateRuntime(ctx, config, "openai")
	if err == nil {
		t.Fatal("corrupt logs database should fail initialization")
	}
	path, ok := RuntimeDBPathForCorruptionError(err)
	if !ok || path != config.LogsDBPath() {
		t.Fatalf("corruption path = %q, %v; error=%v", path, ok, err)
	}
	if _, statErr := os.Stat(config.StateDBPath()); statErr != nil {
		t.Fatalf("state database should remain intact: %v", statErr)
	}
	if filepath.Base(path) != LogsSQLiteFilename {
		t.Fatalf("failed database = %s", path)
	}
}

func TestStateRuntimeAdoptsLegacyGoRemoteControlTable(t *testing.T) {
	for _, hasEnabled := range []bool{false, true} {
		t.Run(map[bool]string{false: "before enabled column", true: "with enabled column"}[hasEnabled], func(t *testing.T) {
			ctx := context.Background()
			config, err := NewSqliteConfig(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			db, err := OpenSQLite(ctx, config.StateDBPath())
			if err != nil {
				t.Fatal(err)
			}
			enabledColumn := ""
			if hasEnabled {
				enabledColumn = ", remote_control_enabled INTEGER"
			}
			if _, err := db.ExecContext(ctx, `
CREATE TABLE remote_control_enrollments (
    websocket_url TEXT NOT NULL,
    account_id TEXT NOT NULL,
    app_server_client_name TEXT NOT NULL,
    server_id TEXT NOT NULL,
    environment_id TEXT NOT NULL,
    server_name TEXT NOT NULL,
    updated_at INTEGER`+enabledColumn+`,
    PRIMARY KEY (websocket_url, account_id, app_server_client_name)
)`); err != nil {
				t.Fatal(err)
			}
			insertColumns := "websocket_url, account_id, app_server_client_name, server_id, environment_id, server_name, updated_at"
			insertValues := "'wss://example.test', 'account', '', 'server', 'environment', 'name', NULL"
			if hasEnabled {
				insertColumns += ", remote_control_enabled"
				insertValues += ", 1"
			}
			if _, err := db.ExecContext(ctx, `INSERT INTO remote_control_enrollments (`+insertColumns+`) VALUES (`+insertValues+`)`); err != nil {
				t.Fatal(err)
			}
			_ = db.Close()

			runtime, err := InitStateRuntime(ctx, config, "openai")
			if err != nil {
				t.Fatalf("InitStateRuntime() adoption error = %v", err)
			}
			defer runtime.Close()
			var serverID string
			var updatedAt int64
			var enabled sql.NullBool
			if err := runtime.StateDB().QueryRowContext(ctx, `
SELECT server_id, updated_at, remote_control_enabled
FROM remote_control_enrollments
WHERE websocket_url = 'wss://example.test' AND account_id = 'account' AND app_server_client_name = ''`).Scan(&serverID, &updatedAt, &enabled); err != nil {
				t.Fatal(err)
			}
			if serverID != "server" || updatedAt != 0 || enabled.Valid != hasEnabled || (enabled.Valid && !enabled.Bool) {
				t.Fatalf("adopted row = server:%q updated:%d enabled:%#v", serverID, updatedAt, enabled)
			}
			var legacyCount int
			if err := runtime.StateDB().QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, legacyRemoteControlTable).Scan(&legacyCount); err != nil || legacyCount != 0 {
				t.Fatalf("legacy staging table count = %d, %v", legacyCount, err)
			}
			var updatedNotNull int
			rows, err := runtime.StateDB().QueryContext(ctx, `PRAGMA table_info(remote_control_enrollments)`)
			if err != nil {
				t.Fatal(err)
			}
			for rows.Next() {
				var cid, notNull, pk int
				var name, typeName string
				var defaultValue any
				if err := rows.Scan(&cid, &name, &typeName, &notNull, &defaultValue, &pk); err != nil {
					t.Fatal(err)
				}
				if name == "updated_at" {
					updatedNotNull = notNull
				}
			}
			_ = rows.Close()
			if updatedNotNull != 1 {
				t.Fatalf("canonical updated_at NOT NULL = %d", updatedNotNull)
			}
		})
	}
}

func TestStateRuntimeRejectsUnknownLegacyGoRemoteControlSchema(t *testing.T) {
	ctx := context.Background()
	config, err := NewSqliteConfig(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	db, err := OpenSQLite(ctx, config.StateDBPath())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `CREATE TABLE remote_control_enrollments (unexpected TEXT)`); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	_, err = InitStateRuntime(ctx, config, "openai")
	if err == nil || !strings.Contains(err.Error(), "unexpected columns") {
		t.Fatalf("unknown legacy schema error = %v", err)
	}
	db, err = OpenSQLite(ctx, config.StateDBPath())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var original, staging int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'remote_control_enrollments'`).Scan(&original); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, legacyRemoteControlTable).Scan(&staging); err != nil {
		t.Fatal(err)
	}
	if original != 1 || staging != 0 {
		t.Fatalf("unknown schema was modified: original=%d staging=%d", original, staging)
	}
}

// TestStateRuntimeRestoresIndependentThreadTimestampMaxima mirrors Rust
// 375996d3f5 (#38893) init_restores_independent_thread_timestamp_maxima: the
// persisted maxima for updated_at_ms and recency_at_ms must be restored
// independently with separate scalar subqueries. When the two maxima belong
// to different threads, a single SELECT MAX(a), MAX(b) evaluates as the
// multi-argument scalar max() and returns the same-row maximum, corrupting one
// counter on reopen.
func TestStateRuntimeRestoresIndependentThreadTimestampMaxima(t *testing.T) {
	ctx := context.Background()
	config, err := NewSqliteConfig(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := InitStateRuntime(ctx, config, "openai")
	if err != nil {
		t.Fatalf("InitStateRuntime() error = %v", err)
	}

	// Two threads: thread A holds the max updated_at_ms, thread B holds the
	// max recency_at_ms. Seed minimal full rows (the schema has NOT NULL
	// columns), then set the timestamp columns so the maxima live in different
	// rows - exactly the Rust regression scenario.
	db := runtime.StateDB()
	for _, row := range []struct {
		id        string
		updatedAt int64
		recencyAt int64
	}{
		{"00000000-0000-0000-0000-000000000101", 3_000, 1_000},
		{"00000000-0000-0000-0000-000000000102", 1_000, 4_000},
	} {
		if _, err := db.ExecContext(ctx,
			`INSERT INTO threads (id, rollout_path, created_at, updated_at, recency_at, created_at_ms, updated_at_ms, recency_at_ms, source, model_provider, cwd, title, sandbox_policy, approval_mode, tokens_used)
			 VALUES (?, '/rollout', 1, 1, 1, 1, 1, 1, 'cli', 'openai', '/repo', 't', 'read-only', 'never', 0)
			 ON CONFLICT(id) DO UPDATE SET updated_at_ms = excluded.updated_at_ms, recency_at_ms = excluded.recency_at_ms`,
			row.id); err != nil {
			t.Fatalf("seed thread %s: %v", row.id, err)
		}
		if _, err := db.ExecContext(ctx,
			`UPDATE threads SET updated_at_ms = ?, recency_at_ms = ? WHERE id = ?`,
			row.updatedAt, row.recencyAt, row.id); err != nil {
			t.Fatalf("update thread %s timestamps: %v", row.id, err)
		}
	}
	if err := runtime.Close(); err != nil {
		t.Fatalf("close runtime: %v", err)
	}

	reopened, err := InitStateRuntime(ctx, config, "openai")
	if err != nil {
		t.Fatalf("reopen runtime: %v", err)
	}
	defer reopened.Close()
	if got := reopened.ThreadUpdatedAtMillis(); got != 3_000 {
		t.Fatalf("restored thread updated_at max = %d, want 3000 (independent subquery)", got)
	}
	if got := reopened.ThreadRecencyAtMillis(); got != 4_000 {
		t.Fatalf("restored thread recency_at max = %d, want 4000 (independent subquery)", got)
	}
}
