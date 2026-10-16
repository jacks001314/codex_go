package state

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	modernsqlite "modernc.org/sqlite"
)

// Mirrors Rust open_read_write_pool_initializes_fresh_database_settings
// (codex-rs/state/src/sqlite_tests.rs, #54389 `1ef30bcb`): a database without a
// schema is initialized with incremental auto-vacuum and WAL, including a file an
// interrupted initialization left with a header but no schema, and the
// connection keeps the WAL durability setting.
func TestOpenReadWritePoolInitializesFreshDatabaseSettingsLikeRust(t *testing.T) {
	ctx := context.Background()
	for _, interrupted := range []bool{false, true} {
		config, err := NewSqliteConfig(t.TempDir())
		if err != nil {
			t.Fatalf("NewSqliteConfig: %v", err)
		}
		path := config.LogsDBPath()
		if interrupted {
			// Auto-vacuum writes a header before WAL setup, so startup must
			// initialize the database even though the file is not empty.
			partial, err := sql.Open("sqlite", path)
			if err != nil {
				t.Fatalf("open partially initialized database: %v", err)
			}
			if _, err := partial.ExecContext(ctx, "PRAGMA auto_vacuum = INCREMENTAL"); err != nil {
				t.Fatalf("PRAGMA auto_vacuum: %v", err)
			}
			if err := partial.Close(); err != nil {
				t.Fatalf("close partially initialized database: %v", err)
			}
			info, err := os.Stat(path)
			if err != nil {
				t.Fatalf("stat partially initialized database: %v", err)
			}
			if info.Size() == 0 {
				t.Fatal("interrupted initialization left an empty file, want a written header")
			}
		}
		db, err := config.OpenReadWrite(ctx, path)
		if err != nil {
			t.Fatalf("OpenReadWrite(interrupted=%v): %v", interrupted, err)
		}
		journal, vacuum, synchronous := readConnectionSettings(t, db)
		if journal != "wal" || vacuum != 2 || synchronous != 1 {
			t.Fatalf("(interrupted=%v) database settings = (%q, %d, %d), want (\"wal\", 2, 1)", interrupted, journal, vacuum, synchronous)
		}
		if err := db.Close(); err != nil {
			t.Fatalf("close pool: %v", err)
		}
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

// Mirrors Rust open_read_write_pool_preserves_delete_under_write_lock: opening an
// initialized DELETE database while another connection holds the writer lock
// succeeds, every pooled connection - including the ones the pool initializes
// later - keeps the DELETE journal with the EXTRA durability setting, and the
// database stays usable and DELETE after reopening.
func TestOpenReadWritePoolPreservesDeleteUnderWriteLockLikeRust(t *testing.T) {
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
	// DELETE is SQLite's default journal mode, so this database needs no WAL setup.
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

	db, err := config.OpenReadWrite(ctx, path)
	if err != nil {
		t.Fatalf("OpenReadWrite under a write lock: %v", err)
	}
	// Keep each acquired connection open to test the connections the pool
	// initializes later, not only the one the opener used.
	connections := make([]*sql.Conn, 0, SQLiteMaxOpenConnections)
	settings := make([]string, 0, SQLiteMaxOpenConnections)
	for index := 0; index < SQLiteMaxOpenConnections; index++ {
		conn, err := db.Conn(ctx)
		if err != nil {
			t.Fatalf("acquire connection %d: %v", index, err)
		}
		journal, _, synchronous := readConnSettings(t, conn)
		settings = append(settings, fmt.Sprintf("%s/%d", journal, synchronous))
		connections = append(connections, conn)
	}
	want := strings.TrimSuffix(strings.Repeat("delete/3,", SQLiteMaxOpenConnections), ",")
	if got := strings.Join(settings, ","); got != want {
		t.Fatalf("pooled connection settings = %s, want %s", got, want)
	}
	for _, conn := range connections {
		if err := conn.Close(); err != nil {
			t.Fatalf("release connection: %v", err)
		}
	}
	if _, err := lockHolder.ExecContext(ctx, "ROLLBACK"); err != nil {
		t.Fatalf("ROLLBACK: %v", err)
	}
	if err := lockHolder.Close(); err != nil {
		t.Fatalf("close lock holder: %v", err)
	}
	if err := existing.Close(); err != nil {
		t.Fatalf("close existing database: %v", err)
	}

	if _, err := db.ExecContext(ctx, "INSERT INTO existing VALUES (42)"); err != nil {
		t.Fatalf("insert into preserved database: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close pool: %v", err)
	}
	reopened, err := config.OpenReadWrite(ctx, path)
	if err != nil {
		t.Fatalf("reopen preserved database: %v", err)
	}
	defer reopened.Close()
	journal, _, _ := readConnectionSettings(t, reopened)
	rows, err := readExistingIDs(ctx, reopened)
	if err != nil {
		t.Fatalf("read preserved rows: %v", err)
	}
	if journal != "delete" || strings.Join(rows, ",") != "42" {
		t.Fatalf("(journal, rows) = (%q, %v), want (\"delete\", [42])", journal, rows)
	}
}

// Mirrors Rust open_read_write_pool_preserves_empty_database_lock_error: a
// database without a schema must initialize WAL, so a held writer surfaces the
// SQLite lock error to the caller, the failed opener releases its own locks, and
// a later startup initializes the database to WAL.
func TestOpenReadWritePoolPreservesEmptyDatabaseLockErrorLikeRust(t *testing.T) {
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
	// Leave the schema empty so startup must initialize WAL; auto-vacuum writes a
	// header first, exactly like an interrupted initialization.
	if _, err := existing.ExecContext(ctx, "PRAGMA auto_vacuum = INCREMENTAL"); err != nil {
		t.Fatalf("PRAGMA auto_vacuum: %v", err)
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
	if openErr == nil {
		t.Fatal("WAL setup must fail while another connection holds the writer lock")
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

	if _, err := lockHolder.ExecContext(ctx, "ROLLBACK"); err != nil {
		t.Fatalf("ROLLBACK: %v", err)
	}
	// A failed opener must release its locks before another writer can proceed.
	if _, err := lockHolder.ExecContext(ctx, "BEGIN EXCLUSIVE"); err != nil {
		t.Fatalf("BEGIN EXCLUSIVE after a failed opener: %v", err)
	}
	if _, err := lockHolder.ExecContext(ctx, "ROLLBACK"); err != nil {
		t.Fatalf("ROLLBACK exclusive: %v", err)
	}
	if err := lockHolder.Close(); err != nil {
		t.Fatalf("close lock holder: %v", err)
	}
	if err := existing.Close(); err != nil {
		t.Fatalf("close existing database: %v", err)
	}

	db, err := config.OpenReadWrite(ctx, path)
	if err != nil {
		t.Fatalf("OpenReadWrite after the lock was released: %v", err)
	}
	defer db.Close()
	if _, err := db.ExecContext(ctx, "CREATE TABLE initialized (id INTEGER PRIMARY KEY)"); err != nil {
		t.Fatalf("create table: %v", err)
	}
	if journal, _, _ := readConnectionSettings(t, db); journal != "wal" {
		t.Fatalf("initialized journal mode = %q, want \"wal\"", journal)
	}
}

func readConnSettings(t *testing.T, conn *sql.Conn) (string, int64, int64) {
	t.Helper()
	ctx := context.Background()
	var journal string
	if err := conn.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&journal); err != nil {
		t.Fatalf("PRAGMA journal_mode: %v", err)
	}
	var vacuum int64
	if err := conn.QueryRowContext(ctx, "PRAGMA auto_vacuum").Scan(&vacuum); err != nil {
		t.Fatalf("PRAGMA auto_vacuum: %v", err)
	}
	var synchronous int64
	if err := conn.QueryRowContext(ctx, "PRAGMA synchronous").Scan(&synchronous); err != nil {
		t.Fatalf("PRAGMA synchronous: %v", err)
	}
	return journal, vacuum, synchronous
}

func readConnectionSettings(t *testing.T, db *sql.DB) (string, int64, int64) {
	t.Helper()
	conn, err := db.Conn(context.Background())
	if err != nil {
		t.Fatalf("acquire connection: %v", err)
	}
	defer conn.Close()
	return readConnSettings(t, conn)
}

func readJournalAndVacuum(t *testing.T, db *sql.DB) (string, int64) {
	t.Helper()
	journal, vacuum, _ := readConnectionSettings(t, db)
	return journal, vacuum
}

func readExistingIDs(ctx context.Context, db *sql.DB) ([]string, error) {
	rows, err := db.QueryContext(ctx, "SELECT id FROM existing ORDER BY id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, strconvFormat(id))
	}
	return ids, rows.Err()
}

func strconvFormat(value int64) string { return fmt.Sprintf("%d", value) }
