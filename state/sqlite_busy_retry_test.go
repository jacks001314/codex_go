package state

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	modernsqlite "modernc.org/sqlite"
)

// Mirrors Rust #54369 (`7ae8319917`), codex-rs/state/src/sqlite/retry_tests.rs.
//
// Every case drives a real competing SQLite writer instead of a synthetic error:
// the writer under test opens with `busy_timeout(0)`, so its first attempt
// returns SQLITE_BUSY immediately rather than waiting out SQLite's default
// five-second busy handler, exactly like the Rust tests' `busy_timeout(ZERO)`.
// Rust synchronises the lock release on the first failed attempt through
// sqlx's `after_release` hook; here the competing writer releases after a short
// delay that is still longer than the first attempt (which never waits) and
// shorter than the 100 ms retry delay, so only the first attempt can be BUSY.

// retryTestReleaseDelay is long enough for the first (non-waiting) attempt to
// have been rejected and short enough that the second attempt finds the lock
// released.
const retryTestReleaseDelay = 50 * time.Millisecond

// retryTestDSN builds a file DSN with an explicit busy timeout.
func retryTestDSN(t *testing.T, path string, busyTimeoutMillis int) string {
	t.Helper()
	absolute, err := filepath.Abs(path)
	if err != nil {
		t.Fatalf("resolve %s: %v", path, err)
	}
	uriPath := filepath.ToSlash(absolute)
	if runtime.GOOS == "windows" && !strings.HasPrefix(uriPath, "/") {
		uriPath = "/" + uriPath
	}
	u := &url.URL{Scheme: "file", Path: uriPath}
	query := u.Query()
	query.Add("_pragma", fmt.Sprintf("busy_timeout(%d)", busyTimeoutMillis))
	u.RawQuery = query.Encode()
	return u.String()
}

// retryTestHome creates an initialized database holding the marker table every
// case writes to, and returns its pool and path.
func retryTestHome(t *testing.T, ctx context.Context) (*sql.DB, string) {
	t.Helper()
	config, err := NewSqliteConfig(t.TempDir())
	if err != nil {
		t.Fatalf("NewSqliteConfig: %v", err)
	}
	path := config.LogsDBPath()
	pool, err := config.OpenReadWrite(ctx, path)
	if err != nil {
		t.Fatalf("OpenReadWrite: %v", err)
	}
	if _, err := pool.ExecContext(ctx, `
CREATE TABLE retry_markers (
    id INTEGER PRIMARY KEY,
    thread_id TEXT NOT NULL,
    body TEXT NOT NULL
)`); err != nil {
		t.Fatalf("create retry marker table: %v", err)
	}
	if err := pool.Close(); err != nil {
		t.Fatalf("close setup pool: %v", err)
	}
	return pool, path
}

// retryTestWriter opens a pool whose writes never wait for a lock.
func retryTestWriter(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", retryTestDSN(t, path, 0))
	if err != nil {
		t.Fatalf("open writer pool: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// heldWriter takes the writer slot and returns a release function.
func heldWriter(t *testing.T, ctx context.Context, path string, releaseAfter time.Duration) func() {
	t.Helper()
	holder, err := sql.Open("sqlite", retryTestDSN(t, path, 0))
	if err != nil {
		t.Fatalf("open competing writer pool: %v", err)
	}
	conn, err := holder.Conn(ctx)
	if err != nil {
		t.Fatalf("acquire competing writer connection: %v", err)
	}
	if _, err := conn.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		t.Fatalf("competing writer BEGIN IMMEDIATE: %v", err)
	}
	released := make(chan struct{})
	go func() {
		defer close(released)
		time.Sleep(releaseAfter)
		if _, err := conn.ExecContext(context.Background(), `ROLLBACK`); err != nil {
			t.Errorf("release competing writer: %v", err)
		}
	}()
	// The returned release function is idempotent, so a test can wait for the
	// slot explicitly and still keep the cleanup call.
	var once sync.Once
	return func() {
		once.Do(func() {
			<-released
			_ = conn.Close()
			_ = holder.Close()
		})
	}
}

// Mirrors Rust retries_after_a_competing_writer_releases_its_lock: the bounded
// retry turns a received BUSY into a success and stores exactly one row.
func TestRetryOnSQLiteBusySucceedsAfterCompetingWriterReleasesLockLikeRust(t *testing.T) {
	ctx := context.Background()
	_, path := retryTestHome(t, ctx)
	writer := retryTestWriter(t, path)
	conn, err := writer.Conn(ctx)
	if err != nil {
		t.Fatalf("acquire writer connection: %v", err)
	}
	defer conn.Close()
	release := heldWriter(t, ctx, path, retryTestReleaseDelay)
	defer release()

	attempts := 0
	if _, err := retryOnSQLiteBusy(func() (struct{}, error) {
		attempts++
		_, execErr := conn.ExecContext(ctx, `INSERT INTO retry_markers (id, thread_id, body) VALUES (1, 'thread-1', 'saved once')`)
		return struct{}{}, execErr
	}); err != nil {
		t.Fatalf("retryOnSQLiteBusy: %v", err)
	}
	if attempts < 2 {
		t.Fatalf("attempts = %d, want at least 2 (the first attempt must have received BUSY)", attempts)
	}
	rows := retryTestBodies(t, ctx, writer)
	if len(rows) != 1 || rows[0] != "saved once" {
		t.Fatalf("stored rows = %q, want exactly [saved once]", rows)
	}
}

// Mirrors Rust queue_enqueue_survives_received_busy_without_duplicating_the_message:
// a standalone write keeps its bound values across the retry and lands once.
func TestExecWriteSurvivesReceivedBusyLikeRust(t *testing.T) {
	ctx := context.Background()
	_, path := retryTestHome(t, ctx)
	writer := retryTestWriter(t, path)
	release := heldWriter(t, ctx, path, retryTestReleaseDelay)
	defer release()

	if _, err := ExecWrite(ctx, writer, `INSERT INTO retry_markers (id, thread_id, body) VALUES (?, ?, ?)`, 7, "thread-1", "save this bound value"); err != nil {
		t.Fatalf("ExecWrite: %v", err)
	}
	rows := retryTestBodies(t, ctx, writer)
	if len(rows) != 1 || rows[0] != "save this bound value" {
		t.Fatalf("stored rows = %q, want exactly [save this bound value]", rows)
	}
	var threadID string
	if err := writer.QueryRowContext(ctx, `SELECT thread_id FROM retry_markers WHERE id = 7`).Scan(&threadID); err != nil {
		t.Fatalf("read stored thread id: %v", err)
	}
	if threadID != "thread-1" {
		t.Fatalf("stored thread id = %q, want %q", threadID, "thread-1")
	}
}

// Mirrors Rust queue_update_survives_received_busy_and_commits_once: the
// RETURNING write commits once and exposes the row it wrote.
func TestQueryWriteRowSurvivesReceivedBusyAndCommitsOnceLikeRust(t *testing.T) {
	ctx := context.Background()
	_, path := retryTestHome(t, ctx)
	writer := retryTestWriter(t, path)
	if _, err := ExecWrite(ctx, writer, `INSERT INTO retry_markers (id, thread_id, body) VALUES (1, 'thread-1', 'original')`); err != nil {
		t.Fatalf("seed marker: %v", err)
	}
	release := heldWriter(t, ctx, path, retryTestReleaseDelay)
	defer release()

	var updated string
	if err := QueryWriteRow(ctx, writer, `UPDATE retry_markers SET body = ? WHERE id = 1 RETURNING body`,
		[]any{"edited bound value"}, func(row *sql.Row) error { return row.Scan(&updated) }); err != nil {
		t.Fatalf("QueryWriteRow: %v", err)
	}
	if updated != "edited bound value" {
		t.Fatalf("returned body = %q, want %q", updated, "edited bound value")
	}
	rows := retryTestBodies(t, ctx, writer)
	if len(rows) != 1 || rows[0] != "edited bound value" {
		t.Fatalf("stored rows = %q, want exactly [edited bound value]", rows)
	}
}

// Mirrors Rust queue_delete_survives_received_busy_and_preserves_other_messages.
func TestQueryWriteRowDeleteSurvivesReceivedBusyAndPreservesOtherRowsLikeRust(t *testing.T) {
	ctx := context.Background()
	_, path := retryTestHome(t, ctx)
	writer := retryTestWriter(t, path)
	for _, seed := range []struct {
		id   int
		body string
	}{{1, "remove"}, {2, "keep"}} {
		if _, err := ExecWrite(ctx, writer, `INSERT INTO retry_markers (id, thread_id, body) VALUES (?, 'thread-1', ?)`, seed.id, seed.body); err != nil {
			t.Fatalf("seed marker %d: %v", seed.id, err)
		}
	}
	release := heldWriter(t, ctx, path, retryTestReleaseDelay)
	defer release()

	var removed string
	if err := QueryWriteRow(ctx, writer, `DELETE FROM retry_markers WHERE id = ? RETURNING body`,
		[]any{1}, func(row *sql.Row) error { return row.Scan(&removed) }); err != nil {
		t.Fatalf("QueryWriteRow: %v", err)
	}
	if removed != "remove" {
		t.Fatalf("returned body = %q, want %q", removed, "remove")
	}
	rows := retryTestBodies(t, ctx, writer)
	if len(rows) != 1 || rows[0] != "keep" {
		t.Fatalf("stored rows = %q, want exactly [keep]", rows)
	}
}

// Mirrors Rust is_busy: only the BUSY result codes are retryable, and a
// non-SQLite error never is.
func TestIsSQLiteBusyErrorMatchesRustBusyCodes(t *testing.T) {
	ctx := context.Background()
	_, path := retryTestHome(t, ctx)
	writer := retryTestWriter(t, path)
	release := heldWriter(t, ctx, path, 200*time.Millisecond)
	defer release()

	_, busyErr := writer.ExecContext(ctx, `INSERT INTO retry_markers (id, thread_id, body) VALUES (3, 'thread-1', 'busy')`)
	if busyErr == nil {
		t.Fatal("competing writer did not reject the write, want SQLITE_BUSY")
	}
	if !isSQLiteBusyError(busyErr) {
		t.Fatalf("isSQLiteBusyError(%v) = false, want true", busyErr)
	}
	var sqliteErr *modernsqlite.Error
	if !errors.As(busyErr, &sqliteErr) {
		t.Fatalf("error %v is not a *modernsqlite.Error", busyErr)
	}
	if code := sqliteErr.Code(); code != sqliteBusyCode {
		t.Fatalf("busy code = %d, want %d", code, sqliteBusyCode)
	}
	if isSQLiteBusyError(errors.New("not a sqlite error")) {
		t.Fatal("isSQLiteBusyError(non-sqlite error) = true, want false")
	}
	// The competing writer has to be gone before a non-BUSY failure is probed.
	release()
	// SQLITE_CONSTRAINT: a permanent failure that must never be retried.
	if _, err := writer.ExecContext(ctx, `INSERT INTO retry_markers (id, thread_id, body) VALUES (NULL, NULL, NULL)`); err == nil {
		t.Fatal("NOT NULL violation succeeded, want SQLITE_CONSTRAINT")
	} else if isSQLiteBusyError(err) {
		t.Fatalf("isSQLiteBusyError(constraint %v) = true, want false", err)
	}
}

// retryTestBodies reads the stored marker bodies in insertion order.
func retryTestBodies(t *testing.T, ctx context.Context, db *sql.DB) []string {
	t.Helper()
	rows, err := db.QueryContext(ctx, `SELECT body FROM retry_markers ORDER BY id`)
	if err != nil {
		t.Fatalf("read markers: %v", err)
	}
	defer rows.Close()
	var bodies []string
	for rows.Next() {
		var body string
		if err := rows.Scan(&body); err != nil {
			t.Fatalf("scan marker: %v", err)
		}
		bodies = append(bodies, body)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate markers: %v", err)
	}
	return bodies
}

// Mirrors Rust is_busy's extended codes: a read snapshot that a competing
// commit invalidated surfaces as SQLITE_BUSY_SNAPSHOT and stays retryable.
func TestIsSQLiteBusyErrorRecognisesBusySnapshotLikeRust(t *testing.T) {
	ctx := context.Background()
	_, path := retryTestHome(t, ctx)
	reader, err := sql.Open("sqlite", retryTestDSN(t, path, 0))
	if err != nil {
		t.Fatalf("open reader pool: %v", err)
	}
	defer reader.Close()
	readerConn, err := reader.Conn(ctx)
	if err != nil {
		t.Fatalf("acquire reader connection: %v", err)
	}
	defer readerConn.Close()
	if _, err := readerConn.ExecContext(ctx, `BEGIN`); err != nil {
		t.Fatalf("reader BEGIN: %v", err)
	}
	var count int
	if err := readerConn.QueryRowContext(ctx, `SELECT COUNT(*) FROM retry_markers`).Scan(&count); err != nil {
		t.Fatalf("reader snapshot read: %v", err)
	}
	committer := retryTestWriter(t, path)
	if _, err := ExecWrite(ctx, committer, `INSERT INTO retry_markers (id, thread_id, body) VALUES (9, 'thread-1', 'committed elsewhere')`); err != nil {
		t.Fatalf("competing commit: %v", err)
	}
	_, snapshotErr := readerConn.ExecContext(ctx, `INSERT INTO retry_markers (id, thread_id, body) VALUES (10, 'thread-1', 'stale snapshot')`)
	if snapshotErr == nil {
		t.Fatal("stale snapshot write succeeded, want SQLITE_BUSY_SNAPSHOT")
	}
	var sqliteErr *modernsqlite.Error
	if !errors.As(snapshotErr, &sqliteErr) {
		t.Fatalf("error %v is not a *modernsqlite.Error", snapshotErr)
	}
	t.Logf("stale snapshot write reported code %d (%v)", sqliteErr.Code(), snapshotErr)
	if !isSQLiteBusyError(snapshotErr) {
		t.Fatalf("isSQLiteBusyError(%v) = false, want true", snapshotErr)
	}
}

// Mirrors Rust's `begin()` -> `begin_immediate()` change: a deferred transaction
// only takes the writer slot at its first write, so that write is the one that
// reports BUSY, while immediate admission retries the slot before any statement
// runs (Rust #54369 `7ae8319917`, `SqlitePoolWriteExt::begin_immediate`).
func TestBeginImmediateTxHoldsWriterSlotFromAdmissionLikeRust(t *testing.T) {
	ctx := context.Background()
	_, path := retryTestHome(t, ctx)
	writer := retryTestWriter(t, path)
	release := heldWriter(t, ctx, path, retryTestReleaseDelay)
	defer release()

	deferred, err := writer.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("deferred BeginTx: %v", err)
	}
	if _, err := deferred.ExecContext(ctx, `INSERT INTO retry_markers (id, thread_id, body) VALUES (4, 'thread-1', 'deferred')`); err == nil {
		_ = deferred.Rollback()
		t.Fatal("deferred transaction wrote while another writer held the slot, want SQLITE_BUSY")
	} else if !isSQLiteBusyError(err) {
		_ = deferred.Rollback()
		t.Fatalf("deferred transaction write = %v, want SQLITE_BUSY", err)
	}
	if err := deferred.Rollback(); err != nil {
		t.Fatalf("rollback deferred transaction: %v", err)
	}

	tx, err := BeginImmediateTx(ctx, writer)
	if err != nil {
		t.Fatalf("BeginImmediateTx: %v", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO retry_markers (id, thread_id, body) VALUES (5, 'thread-1', 'immediate')`); err != nil {
		_ = tx.Rollback()
		t.Fatalf("immediate transaction write: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit immediate transaction: %v", err)
	}
	rows := retryTestBodies(t, ctx, writer)
	if len(rows) != 1 || rows[0] != "immediate" {
		t.Fatalf("stored rows = %q, want exactly [immediate]", rows)
	}
}
