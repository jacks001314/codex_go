package state

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	modernsqlite "modernc.org/sqlite"
)

// Mirrors Rust open_read_write_pool_initializes_fresh_database_settings
// (codex-rs/state/src/sqlite_tests.rs, upstream c2d2f422e6 / #49102): a fresh
// writable database is initialized with incremental auto-vacuum and WAL.
func TestOpenReadWritePoolInitializesFreshDatabaseSettingsLikeRust(t *testing.T) {
	ctx := context.Background()
	config, err := NewSqliteConfig(t.TempDir())
	if err != nil {
		t.Fatalf("NewSqliteConfig: %v", err)
	}
	db, err := config.OpenReadWrite(ctx, config.LogsDBPath())
	if err != nil {
		t.Fatalf("OpenReadWrite: %v", err)
	}
	defer db.Close()
	if mode, vacuum := readJournalAndVacuum(t, db); mode != "wal" || vacuum != 2 {
		t.Fatalf("fresh database settings = (%q, %d), want (\"wal\", 2)", mode, vacuum)
	}
}

// Mirrors Rust open_read_write_pool_preserves_existing_settings_under_write_lock:
// an existing FULL auto-vacuum database keeps its mode, and opening it needs no
// writer lock while another connection holds one.
func TestOpenReadWritePoolPreservesExistingSettingsUnderWriteLockLikeRust(t *testing.T) {
	ctx := context.Background()
	config, err := NewSqliteConfig(t.TempDir())
	if err != nil {
		t.Fatalf("NewSqliteConfig: %v", err)
	}
	path := config.LogsDBPath()
	existing, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open existing database: %v", err)
	}
	defer existing.Close()
	// FULL must be chosen before the first table exists.
	for _, statement := range []string{
		"PRAGMA auto_vacuum = FULL",
		"PRAGMA journal_mode = WAL",
		"CREATE TABLE existing (id INTEGER PRIMARY KEY)",
	} {
		if _, err := existing.ExecContext(ctx, statement); err != nil {
			t.Fatalf("%s: %v", statement, err)
		}
	}
	lockHolder, err := existing.Conn(ctx)
	if err != nil {
		t.Fatalf("pin lock holder: %v", err)
	}
	defer lockHolder.Close()
	if _, err := lockHolder.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		t.Fatalf("BEGIN IMMEDIATE: %v", err)
	}

	db, err := config.OpenReadWrite(ctx, path)
	if err != nil {
		t.Fatalf("OpenReadWrite under a write lock: %v", err)
	}
	defer db.Close()
	if mode, vacuum := readJournalAndVacuum(t, db); mode != "wal" || vacuum != 1 {
		t.Fatalf("preserved database settings = (%q, %d), want (\"wal\", 1)", mode, vacuum)
	}
	if _, err := lockHolder.ExecContext(ctx, "ROLLBACK"); err != nil {
		t.Fatalf("ROLLBACK: %v", err)
	}
}

// Mirrors Rust open_read_write_pool_preserves_wal_conversion_lock_error: the WAL
// conversion under a held writer reports the SQLite lock error to the caller
// (Go has no pooled retry that could mask it as a timeout), and the failure
// releases the opener so a later startup succeeds.
func TestOpenReadWritePoolPreservesWALConversionLockErrorLikeRust(t *testing.T) {
	ctx := context.Background()
	config, err := NewSqliteConfig(t.TempDir())
	if err != nil {
		t.Fatalf("NewSqliteConfig: %v", err)
	}
	path := config.LogsDBPath()
	existing, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open existing database: %v", err)
	}
	defer existing.Close()
	// A non-empty database converted to WAL needs the writer lock (SQLite
	// journal_mode=DELETE is the default, so no WAL setup is needed here).
	if _, err := existing.ExecContext(ctx, "CREATE TABLE existing (id INTEGER PRIMARY KEY)"); err != nil {
		t.Fatalf("create table: %v", err)
	}
	lockHolder, err := existing.Conn(ctx)
	if err != nil {
		t.Fatalf("pin lock holder: %v", err)
	}
	defer lockHolder.Close()
	if _, err := lockHolder.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		t.Fatalf("BEGIN IMMEDIATE: %v", err)
	}

	_, openErr := config.OpenReadWrite(ctx, path)
	if _, err := lockHolder.ExecContext(ctx, "ROLLBACK"); err != nil {
		t.Fatalf("ROLLBACK: %v", err)
	}
	if openErr == nil {
		t.Fatal("WAL conversion requires the writer lock")
	}
	var sqliteErr *modernsqlite.Error
	if !errors.As(openErr, &sqliteErr) {
		t.Fatalf("open error = %v, want a SQLite error", openErr)
	}
	if code := sqliteErr.Code() & 0xff; code != 5 {
		t.Fatalf("open error code = %d, want 5 (SQLITE_BUSY): %v", code, openErr)
	}
	if !IsDBRecoveryLocked(openErr.Error()) {
		t.Fatalf("open error %q is not reported as a lock", openErr)
	}
	// Failure must release the connection so a later startup can succeed.
	db, err := config.OpenReadWrite(ctx, path)
	if err != nil {
		t.Fatalf("OpenReadWrite after the lock was released: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close recovered pool: %v", err)
	}
}

func readJournalAndVacuum(t *testing.T, db *sql.DB) (string, int64) {
	t.Helper()
	conn, err := db.Conn(context.Background())
	if err != nil {
		t.Fatalf("acquire connection: %v", err)
	}
	defer conn.Close()
	var mode string
	if err := conn.QueryRowContext(context.Background(), "PRAGMA journal_mode").Scan(&mode); err != nil {
		t.Fatalf("PRAGMA journal_mode: %v", err)
	}
	var vacuum int64
	if err := conn.QueryRowContext(context.Background(), "PRAGMA auto_vacuum").Scan(&vacuum); err != nil {
		t.Fatalf("PRAGMA auto_vacuum: %v", err)
	}
	return mode, vacuum
}
