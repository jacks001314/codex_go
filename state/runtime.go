package state

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"sync/atomic"
	"time"
)

type runtimeDBSpec struct {
	kind  RuntimeDBKind
	label string
	path  func(SqliteConfig) string
}

var runtimeDBSpecs = []runtimeDBSpec{
	{kind: RuntimeDBState, label: "state DB", path: SqliteConfig.StateDBPath},
	{kind: RuntimeDBLogs, label: "log DB", path: SqliteConfig.LogsDBPath},
	{kind: RuntimeDBGoals, label: "goals DB", path: SqliteConfig.GoalsDBPath},
	{kind: RuntimeDBMemories, label: "memories DB", path: SqliteConfig.MemoriesDBPath},
}

type RuntimeDBInitError struct {
	Label     string
	Operation string
	Path      string
	Err       error
}

func (e *RuntimeDBInitError) Error() string {
	if e == nil {
		return "runtime database initialization failed"
	}
	return fmt.Sprintf("failed to %s %s at %s: %v", e.Operation, e.Label, e.Path, e.Err)
}

func (e *RuntimeDBInitError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

type StateRuntime struct {
	sqlite          SqliteConfig
	defaultProvider string
	stateDB         *sql.DB
	logsDB          *sql.DB
	goalsDB         *sql.DB
	memoriesDB      *sql.DB
	threadHistoryMu sync.Mutex
	threadHistoryDB *sql.DB
	closed          bool
	threadUpdatedAt atomic.Int64
	threadRecencyAt atomic.Int64
}

func InitStateRuntime(ctx context.Context, sqliteConfig SqliteConfig, defaultProvider string) (*StateRuntime, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := os.MkdirAll(sqliteConfig.Home(), 0o700); err != nil {
		return nil, fmt.Errorf("create sqlite home: %w", err)
	}
	opened := make([]*sql.DB, 0, len(runtimeDBSpecs))
	dbs := make(map[RuntimeDBKind]*sql.DB, len(runtimeDBSpecs))
	for _, spec := range runtimeDBSpecs {
		db, err := sqliteConfig.openRuntimeDB(ctx, spec)
		if err != nil {
			closeSQLiteDBs(opened)
			return nil, err
		}
		opened = append(opened, db)
		dbs[spec.kind] = db
	}
	runtime := &StateRuntime{
		sqlite:          sqliteConfig,
		defaultProvider: defaultProvider,
		stateDB:         dbs[RuntimeDBState],
		logsDB:          dbs[RuntimeDBLogs],
		goalsDB:         dbs[RuntimeDBGoals],
		memoriesDB:      dbs[RuntimeDBMemories],
	}
	if err := runtime.ensureBackfillState(ctx); err != nil {
		_ = runtime.Close()
		return nil, err
	}
	if err := runtime.loadThreadTimestamps(ctx); err != nil {
		_ = runtime.Close()
		return nil, err
	}
	if err := runtime.runLogsStartupMaintenance(ctx, time.Now()); err != nil {
		slog.Warn("failed to run startup maintenance for logs database", "path", sqliteConfig.LogsDBPath(), "error", err)
	}
	return runtime, nil
}

func (c SqliteConfig) openRuntimeDB(ctx context.Context, spec runtimeDBSpec) (*sql.DB, error) {
	path := spec.path(c)
	db, err := c.OpenReadWrite(ctx, path)
	if err != nil {
		return nil, &RuntimeDBInitError{Label: spec.label, Operation: "open", Path: path, Err: err}
	}
	if err := migrateRuntimeDB(ctx, db, spec.kind); err != nil {
		_ = db.Close()
		return nil, &RuntimeDBInitError{Label: spec.label, Operation: "migrate", Path: path, Err: err}
	}
	return db, nil
}

func (c SqliteConfig) OpenStateDB(ctx context.Context) (*sql.DB, error) {
	return c.openRuntimeDB(ctx, runtimeDBSpec{kind: RuntimeDBState, label: "state DB", path: SqliteConfig.StateDBPath})
}

func (c SqliteConfig) OpenLogsDB(ctx context.Context) (*sql.DB, error) {
	return c.openRuntimeDB(ctx, runtimeDBSpec{kind: RuntimeDBLogs, label: "log DB", path: SqliteConfig.LogsDBPath})
}

func (c SqliteConfig) OpenGoalsDB(ctx context.Context) (*sql.DB, error) {
	return c.openRuntimeDB(ctx, runtimeDBSpec{kind: RuntimeDBGoals, label: "goals DB", path: SqliteConfig.GoalsDBPath})
}

func (c SqliteConfig) OpenMemoriesDB(ctx context.Context) (*sql.DB, error) {
	return c.openRuntimeDB(ctx, runtimeDBSpec{kind: RuntimeDBMemories, label: "memories DB", path: SqliteConfig.MemoriesDBPath})
}

func (c SqliteConfig) OpenThreadHistoryDB(ctx context.Context) (*sql.DB, error) {
	return c.openRuntimeDB(ctx, runtimeDBSpec{kind: RuntimeDBThreadHistory, label: "thread history DB", path: SqliteConfig.ThreadHistoryDBPath})
}

func (r *StateRuntime) SQLite() SqliteConfig         { return r.sqlite }
func (r *StateRuntime) DefaultProvider() string      { return r.defaultProvider }
func (r *StateRuntime) StateDB() *sql.DB             { return r.stateDB }
func (r *StateRuntime) LogsDB() *sql.DB              { return r.logsDB }
func (r *StateRuntime) GoalsDB() *sql.DB             { return r.goalsDB }
func (r *StateRuntime) MemoriesDB() *sql.DB          { return r.memoriesDB }
func (r *StateRuntime) ThreadUpdatedAtMillis() int64 { return r.threadUpdatedAt.Load() }
func (r *StateRuntime) ThreadRecencyAtMillis() int64 { return r.threadRecencyAt.Load() }

func (r *StateRuntime) Close() error {
	if r == nil {
		return nil
	}
	r.threadHistoryMu.Lock()
	historyDB := r.threadHistoryDB
	r.threadHistoryDB = nil
	r.closed = true
	r.threadHistoryMu.Unlock()
	return errors.Join(
		closeSQLiteDB(historyDB),
		closeSQLiteDB(r.memoriesDB),
		closeSQLiteDB(r.goalsDB),
		closeSQLiteDB(r.logsDB),
		closeSQLiteDB(r.stateDB),
	)
}

func (r *StateRuntime) ThreadHistoryDB(ctx context.Context) (*sql.DB, error) {
	if r == nil {
		return nil, errors.New("state runtime is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	r.threadHistoryMu.Lock()
	defer r.threadHistoryMu.Unlock()
	if r.closed {
		return nil, errors.New("state runtime is closed")
	}
	if r.threadHistoryDB != nil {
		return r.threadHistoryDB, nil
	}
	db, err := r.sqlite.OpenThreadHistoryDB(ctx)
	if err != nil {
		return nil, err
	}
	r.threadHistoryDB = db
	return db, nil
}

func (r *StateRuntime) ensureBackfillState(ctx context.Context) error {
	var exists int
	err := r.stateDB.QueryRowContext(ctx, `SELECT 1 FROM backfill_state WHERE id = 1`).Scan(&exists)
	if err == nil {
		return nil
	}
	if err != sql.ErrNoRows {
		return fmt.Errorf("read backfill state: %w", err)
	}
	_, err = r.stateDB.ExecContext(ctx, `
INSERT INTO backfill_state (id, status, last_watermark, last_success_at, updated_at)
VALUES (1, 'pending', NULL, NULL, ?)
ON CONFLICT(id) DO NOTHING`, time.Now().Unix())
	if err != nil {
		return fmt.Errorf("ensure backfill state: %w", err)
	}
	return nil
}

func (r *StateRuntime) loadThreadTimestamps(ctx context.Context) error {
	var updatedAt, recencyAt sql.NullInt64
	if err := r.stateDB.QueryRowContext(ctx, `SELECT MAX(updated_at_ms), MAX(recency_at_ms) FROM threads`).Scan(&updatedAt, &recencyAt); err != nil {
		return fmt.Errorf("load thread timestamps: %w", err)
	}
	if updatedAt.Valid {
		r.threadUpdatedAt.Store(updatedAt.Int64)
	}
	if recencyAt.Valid {
		r.threadRecencyAt.Store(recencyAt.Int64)
	}
	return nil
}

func closeSQLiteDBs(dbs []*sql.DB) {
	for _, db := range dbs {
		_ = closeSQLiteDB(db)
	}
}

func closeSQLiteDB(db *sql.DB) error {
	if db == nil {
		return nil
	}
	return db.Close()
}
