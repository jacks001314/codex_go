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
	// recovery decides what a confirmed quick-check corruption finding may do
	// (Rust `RecoveryMode`, upstream 3620b2caf8 / #49701).
	recovery dbRecoveryMode
	// backgroundReclamation opts the database into the background
	// incremental-vacuum worker (Rust `RuntimeDbSpec::background_reclamation`,
	// upstream 33a0f766a6 / #49069).
	backgroundReclamation bool
}

// dbRecoveryMode mirrors Rust `RecoveryMode`.
type dbRecoveryMode int

const (
	// dbRecoveryBackupAndRebuild preserves the damaged files and lets the
	// runtime rebuild the database from saved data. It is the policy for every
	// runtime database that supports recovery.
	dbRecoveryBackupAndRebuild dbRecoveryMode = iota
	// dbRecoveryUnavailable reports corruption without rebuilding. Rust gives
	// thread history this policy: unrelated corruption must not disable lazy
	// history reads.
	dbRecoveryUnavailable
)

// SetMetrics installs the TaskMetrics used to emit SQLite log-persistence
// telemetry (Rust #40726 codex.sqlite.log.write_*).
func (r *StateRuntime) SetMetrics(metrics *TaskMetrics) {
	if r == nil {
		return
	}
	r.metrics = metrics
}

var runtimeDBSpecs = []runtimeDBSpec{
	{kind: RuntimeDBState, label: "state DB", path: SqliteConfig.StateDBPath},
	// Log transactions write before reading, so they already hold the writer
	// lock and an intervening reclamation commit cannot turn their deferred
	// read-to-write upgrade into SQLITE_BUSY_SNAPSHOT (upstream 33a0f766a6 /
	// #49069). Every other runtime database keeps reclamation disabled.
	{kind: RuntimeDBLogs, label: "log DB", path: SqliteConfig.LogsDBPath, backgroundReclamation: true},
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
	memoriesV2Mu    sync.Mutex
	memoriesV2DB    *sql.DB
	// ownsDBs is true only for the runtime that opened the shared databases.
	// Version-scoped memory store views share those handles and must not close
	// them.
	ownsDBs         bool
	threadHistoryMu sync.Mutex
	threadHistoryDB *sql.DB
	metrics         *TaskMetrics
	closed          bool
	// logsMaintenanceMu guards the background diagnostic-log pruning tasks
	// (Rust #49425, upstream 8ea2c0e0d4).
	logsMaintenanceMu   sync.Mutex
	logsMaintenanceDone chan struct{}
	logsMaintenanceWG   sync.WaitGroup
	// reclamation owns the background incremental-vacuum worker for the
	// databases that opted in (Rust #49069, upstream 33a0f766a6). It is nil when
	// no database opted in.
	reclamation     *sqliteReclamationWorker
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
		ownsDBs:         true,
	}
	// Rust spawns the reclamation worker as the runtime is constructed and
	// stops it in `StateRuntime::close` (upstream 33a0f766a6 / #49069).
	runtime.reclamation = spawnSqliteReclamationWorker(sqliteConfig)
	if err := runtime.ensureBackfillState(ctx); err != nil {
		_ = runtime.Close()
		return nil, err
	}
	if err := runtime.loadThreadTimestamps(ctx); err != nil {
		_ = runtime.Close()
		return nil, err
	}
	// Rust #49425: the diagnostic-log sweep runs immediately and then on a
	// timer instead of only during startup.
	runtime.startPeriodicLogsMaintenance(logsMaintenancePeriod)
	return runtime, nil
}

func (c SqliteConfig) openRuntimeDB(ctx context.Context, spec runtimeDBSpec) (*sql.DB, error) {
	path := spec.path(c)
	db, err := c.OpenReadWrite(ctx, path)
	if err != nil {
		return nil, &RuntimeDBInitError{Label: spec.label, Operation: "open", Path: path, Err: err}
	}
	// A database can open successfully while containing corruption, so writable
	// databases are validated with `PRAGMA quick_check(1)` before migrations run
	// (Rust #49701, upstream 3620b2caf8, `open_read_write_pool_with_spec`).
	outcome, validateErr := c.quickCheckDatabase(ctx, db, path)
	if validateErr != nil {
		_ = db.Close()
		return nil, &RuntimeDBInitError{Label: spec.label, Operation: "open", Path: path, Err: validateErr}
	}
	if outcome == QuickCheckCorruptedNeedsFixed {
		logQuickCheckCorruption(path, spec.label)
		// Rust records the finding before the policy branch, so a report-only
		// database (thread history) is counted even though it is not rebuilt.
		recordDBCorruption(c.corruptionMetrics, spec.kind)
		if spec.recovery == dbRecoveryBackupAndRebuild {
			// Preserve the damaged files, then reconnect so the runtime rebuilds
			// the database from scratch (Rust `open_read_write_pool_with_spec`).
			// The rebuilt database is deliberately not re-validated: it was just
			// created, and a later open checks its new file identity.
			_ = db.Close()
			backups, backupErr := BackupDBFilesForFreshStart(&DBRecoveryStartupError{
				DatabasePath: path,
				Detail:       "PRAGMA quick_check(1) reported corruption",
			}, time.Time{})
			if backupErr != nil {
				return nil, &RuntimeDBInitError{Label: spec.label, Operation: "recover", Path: path, Err: backupErr}
			}
			for _, backup := range backups {
				slog.Warn("preserved corrupt sqlite database before rebuilding",
					"database", backup.OriginalPath, "backup", backup.BackupPath)
			}
			c.recoveryCollector.Record(backups...)
			rebuilt, reopenErr := c.OpenReadWrite(ctx, path)
			if reopenErr != nil {
				return nil, &RuntimeDBInitError{Label: spec.label, Operation: "open", Path: path, Err: reopenErr}
			}
			db = rebuilt
		}
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

// OpenMemoriesV2DB lazily opens the isolated v2 memories database, reusing the
// memories schema (Rust #43797 open_memories_v2_db).
func (c SqliteConfig) OpenMemoriesV2DB(ctx context.Context) (*sql.DB, error) {
	return c.openRuntimeDB(ctx, runtimeDBSpec{kind: RuntimeDBMemories, label: "memories v2 DB", path: SqliteConfig.MemoriesV2DBPath})
}

func (c SqliteConfig) OpenThreadHistoryDB(ctx context.Context) (*sql.DB, error) {
	return c.openRuntimeDB(ctx, runtimeDBSpec{
		kind:     RuntimeDBThreadHistory,
		label:    "thread history DB",
		path:     SqliteConfig.ThreadHistoryDBPath,
		recovery: dbRecoveryUnavailable,
	})
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
	// Version-scoped memory store views share the owning runtime's database
	// handles and must not close them.
	if r == nil || !r.ownsDBs {
		return nil
	}
	r.threadHistoryMu.Lock()
	historyDB := r.threadHistoryDB
	alreadyClosed := r.closed
	r.threadHistoryDB = nil
	r.closed = true
	r.threadHistoryMu.Unlock()
	// Stop the reclamation worker first: it holds a dedicated SQLite connection
	// while a pass is in flight (Rust `StateRuntime::close` closes reclamation
	// before the pools, upstream 33a0f766a6 / #49069).
	r.reclamation.close()
	// Stop the background log maintenance and let an in-flight sweep finish
	// before the pools are closed.
	//
	// Documented difference: Rust's task upgrades a `Weak<StateRuntime>` and
	// stops on a closed `logs_pool`; Go keeps the runtime alive in the task, so
	// Close signals the done channel first and then waits here.
	r.logsMaintenanceMu.Lock()
	if !alreadyClosed && r.logsMaintenanceDone != nil {
		close(r.logsMaintenanceDone)
	}
	r.logsMaintenanceWG.Wait()
	r.logsMaintenanceMu.Unlock()
	r.memoriesV2Mu.Lock()
	memoriesV2DB := r.memoriesV2DB
	r.memoriesV2DB = nil
	r.memoriesV2Mu.Unlock()
	return errors.Join(
		closeSQLiteDB(memoriesV2DB),
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
	// Rust 375996d3f5 (#38893): the persisted maxima for updated_at_ms and
	// recency_at_ms must be loaded with separate scalar subqueries. A single
	// SELECT MAX(a), MAX(b) evaluates as the multi-argument scalar max() and
	// returns the same-row maximum when the two maxima belong to different
	// threads, silently corrupting one timestamp counter. Independent
	// subqueries restore each counter from its own column.
	if err := r.stateDB.QueryRowContext(ctx,
		`SELECT (SELECT MAX(updated_at_ms) FROM threads), (SELECT MAX(recency_at_ms) FROM threads)`,
	).Scan(&updatedAt, &recencyAt); err != nil {
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
