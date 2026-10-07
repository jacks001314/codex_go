package state

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// seedValidationDB creates a database with `rows` rows in `sample`.
func seedValidationDB(t *testing.T, path string, rows int) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open seed db: %v", err)
	}
	defer db.Close()
	ctx := context.Background()
	if _, err := db.ExecContext(ctx, "CREATE TABLE sample(value INTEGER)"); err != nil {
		t.Fatalf("create sample: %v", err)
	}
	for i := 1; i <= rows; i++ {
		if _, err := db.ExecContext(ctx, "INSERT INTO sample VALUES (?)", i); err != nil {
			t.Fatalf("insert sample %d: %v", i, err)
		}
	}
}

// corruptValidationDB makes `PRAGMA quick_check(1)` report a finding while the
// database still opens: `sqlite_schema` is rewritten to claim a nullable column
// is NOT NULL, exactly like the upstream fixture in
// codex-rs/state/src/sqlite/validation_tests.rs (Rust #49701).
func corruptValidationDB(t *testing.T, path string) {
	t.Helper()
	seedValidationDB(t, path, 0)
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open fixture db: %v", err)
	}
	defer db.Close()
	ctx := context.Background()
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatalf("pin fixture connection: %v", err)
	}
	defer conn.Close()
	statements := []string{
		"INSERT INTO sample VALUES (NULL)",
		"PRAGMA writable_schema=ON",
		"UPDATE sqlite_schema SET sql='CREATE TABLE sample(value INTEGER NOT NULL)' WHERE name='sample'",
		"PRAGMA writable_schema=OFF",
	}
	for _, statement := range statements {
		if _, err := conn.ExecContext(ctx, statement); err != nil {
			t.Fatalf("%s: %v", statement, err)
		}
	}
}

func openValidationDB(t *testing.T, path string) *sql.DB {
	t.Helper()
	var config SqliteConfig
	var err error
	if config, err = NewSqliteConfig(filepath.Dir(path)); err != nil {
		t.Fatalf("NewSqliteConfig: %v", err)
	}
	db, err := config.OpenReadWrite(context.Background(), path)
	if err != nil {
		t.Fatalf("OpenReadWrite(%s): %v", path, err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// Mirrors Rust quick_check_failure_is_recovered_before_runtime_init_returns'
// core classification: a clean file is Complete, the corrupted fixture is
// CorruptedNeedsFixed.
func TestQuickCheckClassifiesCorruptionLikeRust(t *testing.T) {
	ctx := context.Background()
	clean := filepath.Join(t.TempDir(), "state_5.sqlite")
	seedValidationDB(t, clean, 4)
	if got := quickCheck(ctx, openValidationDB(t, clean), 5*time.Second); got != QuickCheckComplete {
		t.Fatalf("clean quick_check = %v, want %v", got, QuickCheckComplete)
	}

	corrupt := filepath.Join(t.TempDir(), "state_5.sqlite")
	corruptValidationDB(t, corrupt)
	if got := quickCheck(ctx, openValidationDB(t, corrupt), 5*time.Second); got != QuickCheckCorruptedNeedsFixed {
		t.Fatalf("corrupt quick_check = %v, want %v", got, QuickCheckCorruptedNeedsFixed)
	}
}

// Mirrors Rust corruption_attempts_are_cached_per_owner and
// repeated_pool_opens_share_completed_and_incomplete_attempts: attempts are
// cached per owner and per file identity, so replacing the file at the same path
// is scanned again while an independent owner checks independently.
func TestQuickCheckAttemptsAreCachedPerFileIdentityLikeRust(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	path := filepath.Join(home, "state_5.sqlite")
	seedValidationDB(t, path, 4)
	manager := &sqliteQuickCheckManager{}

	db := openValidationDB(t, path)
	if got, err := manager.quickCheckOnce(ctx, db, path, 5*time.Second); err != nil || got != QuickCheckComplete {
		t.Fatalf("first attempt = %v, %v", got, err)
	}
	if got, err := manager.quickCheckOnce(ctx, db, path, 5*time.Second); err != nil || got != QuickCheckSkipped {
		t.Fatalf("repeated attempt = %v, %v, want %v", got, err, QuickCheckSkipped)
	}
	// A closed pool proves the cached attempt skips without another query.
	if err := db.Close(); err != nil {
		t.Fatalf("close db: %v", err)
	}
	if got, err := manager.quickCheckOnce(ctx, db, path, 5*time.Second); err != nil || got != QuickCheckSkipped {
		t.Fatalf("cached attempt on closed pool = %v, %v, want %v", got, err, QuickCheckSkipped)
	}

	// Replacing the file at the same path is a new file identity.
	fixture := filepath.Join(home, "replacement.sqlite")
	corruptValidationDB(t, fixture)
	if err := os.Rename(fixture, path); err != nil {
		t.Fatalf("replace validated file: %v", err)
	}
	replaced := openValidationDB(t, path)
	if got, err := manager.quickCheckOnce(ctx, replaced, path, 5*time.Second); err != nil || got != QuickCheckCorruptedNeedsFixed {
		t.Fatalf("replaced file attempt = %v, %v, want %v", got, err, QuickCheckCorruptedNeedsFixed)
	}
	// Another owner checks the same file independently.
	independent := &sqliteQuickCheckManager{}
	if got, err := independent.quickCheckOnce(ctx, replaced, path, 5*time.Second); err != nil || got != QuickCheckCorruptedNeedsFixed {
		t.Fatalf("independent owner attempt = %v, %v, want %v", got, err, QuickCheckCorruptedNeedsFixed)
	}
}

// Mirrors Rust repeated_pool_opens_share_completed_and_incomplete_attempts'
// clone semantics: copies share the attempt cache, a new config does not.
func TestQuickCheckManagerSharedAcrossConfigClonesLikeRust(t *testing.T) {
	home := t.TempDir()
	config, err := NewSqliteConfig(home)
	if err != nil {
		t.Fatalf("NewSqliteConfig: %v", err)
	}
	clone := config
	if config.quickCheck == nil || clone.quickCheck != config.quickCheck {
		t.Fatalf("config clone does not share the quick-check manager")
	}
	fresh, err := NewSqliteConfig(home)
	if err != nil {
		t.Fatalf("NewSqliteConfig: %v", err)
	}
	if fresh.quickCheck == config.quickCheck {
		t.Fatalf("a freshly constructed config must validate independently")
	}
}

// Mirrors Rust cancelled_check_counts_as_an_attempt: an interrupted attempt is
// still recorded, so the next open skips validation.
func TestQuickCheckInterruptedAttemptCountsLikeRust(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "state_5.sqlite")
	seedValidationDB(t, path, 4)
	manager := &sqliteQuickCheckManager{}
	db := openValidationDB(t, path)

	if got, err := manager.quickCheckOnce(ctx, db, path, 0); err != nil || got != QuickCheckIncomplete {
		t.Fatalf("expired-budget attempt = %v, %v, want %v", got, err, QuickCheckIncomplete)
	}
	if got, err := manager.quickCheckOnce(ctx, db, path, 5*time.Second); err != nil || got != QuickCheckSkipped {
		t.Fatalf("attempt after interruption = %v, %v, want %v", got, err, QuickCheckSkipped)
	}
}

// Mirrors Rust interrupted_check_leaves_connection_usable: an interrupted scan
// must not poison the connection that ran it. OpenReadOnly pins MaxOpenConns(1),
// so the follow-up query necessarily reuses the interrupted connection.
func TestInterruptedQuickCheckLeavesConnectionUsableLikeRust(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "logs_2.sqlite")
	seedValidationDB(t, path, 2048)

	var config SqliteConfig
	var err error
	if config, err = NewSqliteConfig(filepath.Dir(path)); err != nil {
		t.Fatalf("NewSqliteConfig: %v", err)
	}
	reader, err := config.OpenReadOnly(ctx, path)
	if err != nil {
		t.Fatalf("OpenReadOnly: %v", err)
	}
	defer reader.Close()

	if got := quickCheck(ctx, reader, 0); got != QuickCheckIncomplete {
		t.Fatalf("interrupted quick_check = %v, want %v", got, QuickCheckIncomplete)
	}
	var sum int64
	if err := reader.QueryRowContext(ctx, "SELECT sum(value) FROM sample").Scan(&sum); err != nil {
		t.Fatalf("connection unusable after interrupt: %v", err)
	}
	if sum != 2_098_176 {
		t.Fatalf("sum after interrupt = %d, want 2098176", sum)
	}
	if got := quickCheck(ctx, reader, 5*time.Second); got != QuickCheckComplete {
		t.Fatalf("quick_check after interrupt = %v, want %v", got, QuickCheckComplete)
	}
}

// Mirrors Rust locked_database_does_not_count_as_corruption: a locked database
// is incomplete, never corruption.
func TestQuickCheckLockedDatabaseIsIncompleteLikeRust(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "logs_2.sqlite")
	seedValidationDB(t, path, 4)
	config, err := NewSqliteConfig(filepath.Dir(path))
	if err != nil {
		t.Fatalf("NewSqliteConfig: %v", err)
	}

	writer, err := config.OpenReadWrite(ctx, path)
	if err != nil {
		t.Fatalf("OpenReadWrite: %v", err)
	}
	defer writer.Close()
	writerConn, err := writer.Conn(ctx)
	if err != nil {
		t.Fatalf("pin writer connection: %v", err)
	}
	defer writerConn.Close()
	if _, err := writerConn.ExecContext(ctx, "PRAGMA journal_mode=DELETE"); err != nil {
		t.Fatalf("journal_mode=DELETE: %v", err)
	}
	reader, err := config.OpenReadOnly(ctx, path)
	if err != nil {
		t.Fatalf("OpenReadOnly: %v", err)
	}
	defer reader.Close()
	if _, err := writerConn.ExecContext(ctx, "BEGIN EXCLUSIVE"); err != nil {
		t.Fatalf("BEGIN EXCLUSIVE: %v", err)
	}
	locked := quickCheck(ctx, reader, 50*time.Millisecond)
	if _, err := writerConn.ExecContext(ctx, "ROLLBACK"); err != nil {
		t.Fatalf("ROLLBACK: %v", err)
	}
	if locked != QuickCheckIncomplete {
		t.Fatalf("locked quick_check = %v, want %v", locked, QuickCheckIncomplete)
	}
	if got := quickCheck(ctx, reader, 5*time.Second); got != QuickCheckComplete {
		t.Fatalf("quick_check after unlock = %v, want %v", got, QuickCheckComplete)
	}
}

// The open path validates the freshly opened database before migrations run:
// opening a corrupted database records exactly one validation attempt for that
// file identity, and the runtime recovery policy then replaces the damaged file
// (Rust #49701 `open_read_write_pool_with_spec`).
func TestOpenRuntimeDBValidatesBeforeMigrateLikeRust(t *testing.T) {
	ctx := context.Background()
	config, err := NewSqliteConfig(t.TempDir())
	if err != nil {
		t.Fatalf("NewSqliteConfig: %v", err)
	}
	corruptValidationDB(t, config.StateDBPath())
	db, err := config.OpenStateDB(ctx)
	if err != nil {
		t.Fatalf("OpenStateDB on corrupt fixture: %v", err)
	}
	defer db.Close()
	if len(config.quickCheck.seen) != 1 {
		t.Fatalf("validation attempts = %d, want 1", len(config.quickCheck.seen))
	}
	// The returned pool is the rebuilt database, not the damaged file.
	if got := quickCheck(ctx, db, 5*time.Second); got != QuickCheckComplete {
		t.Fatalf("rebuilt state db quick_check = %v, want %v", got, QuickCheckComplete)
	}
	// The rebuilt file has a new identity, so the next open validates again
	// (Rust "replacement files are checked again").
	if err := db.Close(); err != nil {
		t.Fatalf("close rebuilt state db: %v", err)
	}
	reopened, err := config.OpenStateDB(ctx)
	if err != nil {
		t.Fatalf("reopen state db: %v", err)
	}
	defer reopened.Close()
	if len(config.quickCheck.seen) != 2 {
		t.Fatalf("validation attempts after reopen = %d, want 2", len(config.quickCheck.seen))
	}
	if got := quickCheck(ctx, reopened, 5*time.Second); got != QuickCheckComplete {
		t.Fatalf("reopened state db quick_check = %v, want %v", got, QuickCheckComplete)
	}
}
