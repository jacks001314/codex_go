package state

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	modernsqlite "modernc.org/sqlite"
)

// Bounded retries for SQLite operations that can safely restart on external
// contention (Rust #54369 `7ae8319917`,
// codex-rs/state/src/sqlite/busy_retry.rs). Callers must complete one atomic
// transaction - or only open a connection - per attempt. Cancellation is never
// retried: a commit whose result was not received may have succeeded.

// sqliteBusyRetryDelays bounds the attempts made after a received SQLITE_BUSY.
// SQLite already waits up to five seconds per lock (the DSN's
// `busy_timeout(5000)`), so two more attempts with 100 ms and 200 ms match the
// Rust delays.
var sqliteBusyRetryDelays = []time.Duration{100 * time.Millisecond, 200 * time.Millisecond}

// SQLite result codes that describe writer contention rather than a permanent
// failure: SQLITE_BUSY, SQLITE_BUSY_RECOVERY, SQLITE_BUSY_SNAPSHOT and
// SQLITE_BUSY_TIMEOUT. modernc reports the extended code, so every form is
// accepted, exactly like Rust's `is_busy`.
const (
	sqliteBusyCode         = 5
	sqliteBusyRecoveryCode = 261
	sqliteBusySnapshotCode = 517
	sqliteBusyTimeoutCode  = 773
)

// isSQLiteBusyError reports whether err is a received SQLITE_BUSY error.
func isSQLiteBusyError(err error) bool {
	var sqliteErr *modernsqlite.Error
	if !errors.As(err, &sqliteErr) {
		return false
	}
	switch sqliteErr.Code() {
	case sqliteBusyCode, sqliteBusyRecoveryCode, sqliteBusySnapshotCode, sqliteBusyTimeoutCode:
		return true
	default:
		return false
	}
}

// retryOnSQLiteBusy runs operation until it succeeds or the bounded delays are
// exhausted, retrying only when the operation reported SQLITE_BUSY. Every retry
// calls operation again, so an operation must own its whole attempt and leave no
// transaction open when it fails.
func retryOnSQLiteBusy[T any](operation func() (T, error)) (T, error) {
	var zero T
	if operation == nil {
		return zero, errors.New("sqlite retry operation is required")
	}
	attempt := 0
	for {
		value, err := operation()
		if err == nil {
			return value, nil
		}
		if !isSQLiteBusyError(err) || attempt >= len(sqliteBusyRetryDelays) {
			return zero, err
		}
		time.Sleep(sqliteBusyRetryDelays[attempt])
		attempt++
	}
}

// BeginImmediate acquires the writer slot for a write transaction before any
// read snapshot is taken, retrying only admission when SQLite reports BUSY
// (Rust #54369 `7ae8319917`, `SqlitePoolWriteExt::begin_immediate`). An
// already-started transaction is never replayed: a failed attempt closes its own
// connection, which rolls the transaction back.
//
// The caller owns the returned connection and must COMMIT or ROLLBACK and close
// it, exactly as with `sql.DB.Conn`.
func BeginImmediate(ctx context.Context, db *sql.DB) (*sql.Conn, error) {
	if db == nil {
		return nil, errors.New("sqlite database is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return retryOnSQLiteBusy(func() (*sql.Conn, error) {
		conn, err := db.Conn(ctx)
		if err != nil {
			return nil, err
		}
		if _, err := conn.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
			// Closing the connection rolls back the failed attempt and returns the
			// slot, so the next attempt starts from a clean writer state.
			_ = conn.Close()
			return nil, err
		}
		return conn, nil
	})
}

// sqliteExecer is the write surface ExecWrite needs: a *sql.DB for a pooled
// autocommit write, or a *sql.Conn whose migrations path already owns the
// connection. Rust #54369 (`7ae8319917`) retries every standalone
// `execute_with_busy_retry`, including the migration connection
// (`codex-rs/state/src/migrations.rs:113`).
type sqliteExecer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// sqliteWriteTx is the transaction surface the state write paths use. Both
// *sql.Tx and *ImmediateTx satisfy it, so one helper serves either begin form.
type sqliteWriteTx interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// ExecWrite runs one standalone autocommit write, retrying with exactly the same
// bound arguments (Rust #54369 `7ae8319917`,
// `SqliteWriteQueryExt::execute_with_busy_retry`). It must not be used for more
// than one statement or inside an existing transaction, which owns its own
// connection.
func ExecWrite(ctx context.Context, execer sqliteExecer, query string, args ...any) (sql.Result, error) {
	if execer == nil {
		return nil, errors.New("sqlite executor is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return retryOnSQLiteBusy(func() (sql.Result, error) {
		return execer.ExecContext(ctx, query, args...)
	})
}

// ImmediateTx is the Go form of Rust's `SqlitePoolWriteExt::begin_immediate`
// transaction handle: the writer slot is held from the first statement, so a
// competing writer cannot turn this transaction's first write into SQLITE_BUSY,
// and only admission is retried while SQLite reports BUSY (Rust #54369
// `7ae8319917`, `codex-rs/state/src/sqlite/busy_retry.rs:26`).
type ImmediateTx struct {
	conn *sql.Conn
	done bool
}

// BeginImmediateTx opens an immediate write transaction, retrying only the
// admission attempt. The caller owns the returned handle and must Commit or
// Rollback it.
func BeginImmediateTx(ctx context.Context, db *sql.DB) (*ImmediateTx, error) {
	conn, err := BeginImmediate(ctx, db)
	if err != nil {
		return nil, err
	}
	return &ImmediateTx{conn: conn}, nil
}

func (t *ImmediateTx) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	return t.conn.ExecContext(ctx, query, args...)
}

func (t *ImmediateTx) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	return t.conn.QueryContext(ctx, query, args...)
}

func (t *ImmediateTx) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	return t.conn.QueryRowContext(ctx, query, args...)
}

// Commit finishes the transaction and releases its connection. The statement
// itself runs on a background context, like database/sql, so a cancelled caller
// context cannot leave the writer slot held.
func (t *ImmediateTx) Commit() error {
	if t == nil || t.done {
		return sql.ErrTxDone
	}
	t.done = true
	_, err := t.conn.ExecContext(context.Background(), `COMMIT`)
	if closeErr := t.conn.Close(); err == nil {
		err = closeErr
	}
	return err
}

// Rollback releases the writer slot. It is a no-op after Commit, exactly like
// database/sql's ErrTxDone.
func (t *ImmediateTx) Rollback() error {
	if t == nil || t.done {
		return sql.ErrTxDone
	}
	t.done = true
	_, err := t.conn.ExecContext(context.Background(), `ROLLBACK`)
	if closeErr := t.conn.Close(); err == nil {
		err = closeErr
	}
	return err
}

// QueryWriteRow reserves the writer slot with BEGIN IMMEDIATE, decodes the row
// the write returns and commits before the value is exposed (Rust #54369
// `7ae8319917`, `SqliteWriteQueryExt::fetch_one_write`). A decode failure rolls
// the write back, and a commit that fails surfaces its own error rather than a
// duplicated write.
func QueryWriteRow(ctx context.Context, db *sql.DB, query string, args []any, scan func(*sql.Row) error) error {
	if scan == nil {
		return fmt.Errorf("sqlite write row scan is required")
	}
	conn, err := BeginImmediate(ctx, db)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()
	row := conn.QueryRowContext(ctx, query, args...)
	if err := scan(row); err != nil {
		// A failed decode must not leave the writer slot open.
		_, _ = conn.ExecContext(context.Background(), `ROLLBACK`)
		return err
	}
	if _, err := conn.ExecContext(ctx, `COMMIT`); err != nil {
		_, _ = conn.ExecContext(context.Background(), `ROLLBACK`)
		return err
	}
	return nil
}

// scanThreadGoalWrite decodes a RETURNING row before QueryWriteRow commits the
// write that produced it (Rust #54369 `7ae8319917`,
// `SqliteWriteQueryExt::fetch_one_write`).
func scanThreadGoalWrite(target **ThreadGoal) func(*sql.Row) error {
	return func(row *sql.Row) error {
		value, err := scanThreadGoal(row)
		if err != nil {
			return err
		}
		*target = value
		return nil
	}
}
