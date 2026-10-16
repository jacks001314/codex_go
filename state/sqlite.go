package state

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"

	modernsqlite "modernc.org/sqlite"
)

const (
	MinimumSQLiteVersion   = "3.51.3"
	StateSQLiteFilename    = "state_5.sqlite"
	LogsSQLiteFilename     = "logs_2.sqlite"
	GoalsSQLiteFilename    = "goals_1.sqlite"
	MemoriesSQLiteFilename = "memories_1.sqlite"
	// MemoriesV2SQLiteFilename is the lazily-created isolated v2 memories DB
	// (Rust #43797 MEMORIES_V2_DB); the thread catalog stays in the state DB.
	MemoriesV2SQLiteFilename    = "memories_v2_1.sqlite"
	ThreadHistorySQLiteFilename = "thread_history_1.sqlite"
	SQLiteMaxOpenConnections    = 5

	// sqliteJournalModeDelete is SQLite's rollback-journal mode. Rust #54389
	// (`1ef30bcb`) preserves the journal mode of an initialized database and
	// raises `synchronous` to EXTRA for it, because EXTRA additionally syncs the
	// directory after SQLite deletes a rollback journal.
	sqliteJournalModeDelete = "delete"
	// sqliteJournalModeWAL is the mode a database without a schema is
	// initialized to, and the only mode the background reclamation worker runs
	// in (Rust #54389 `codex-rs/state/src/sqlite.rs:364`).
	sqliteJournalModeWAL = "wal"
)

type RuntimeDBKind string

const (
	RuntimeDBState         RuntimeDBKind = "state"
	RuntimeDBLogs          RuntimeDBKind = "logs"
	RuntimeDBGoals         RuntimeDBKind = "goals"
	RuntimeDBMemories      RuntimeDBKind = "memories"
	RuntimeDBThreadHistory RuntimeDBKind = "thread_history"
)

type RuntimeDBPath struct {
	Label string
	Path  string
	// BackgroundReclamation reports whether the background incremental-vacuum
	// worker owns this database (Rust `RuntimeDbPath::background_reclamation`,
	// upstream 33a0f766a6 / #49069). Only the logs database opts in.
	BackgroundReclamation bool
}

// sqliteFilesystemProbe caches the filesystem probe a SqliteConfig shares with
// its copies. Rust stores it in an `Arc<OnceCell<bool>>` so clones reuse one
// probe and a new config probes independently, while a failed probe is retried
// (Rust #54417 `9c84e89c`, `SqliteConfig::is_nfs`).
type sqliteFilesystemProbe struct {
	probe func(path string) (bool, error)

	mu    sync.Mutex
	done  bool
	value bool
}

func newSqliteFilesystemProbe(probe func(path string) (bool, error)) *sqliteFilesystemProbe {
	if probe == nil {
		probe = isNFSPath
	}
	return &sqliteFilesystemProbe{probe: probe}
}

// isNFS probes the path once and caches a successful result. A failed probe is
// not cached, so a home that appears later is still detected.
func (p *sqliteFilesystemProbe) isNFS(path string) (bool, error) {
	if p == nil {
		return isNFSPath(path)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.done {
		return p.value, nil
	}
	value, err := p.probe(path)
	if err != nil {
		return false, err
	}
	p.done = true
	p.value = value
	return value, nil
}

// SqliteConfig is the single resolved home used by every Codex runtime DB.
//
// Copies share the quick-check attempt cache, exactly like Rust `SqliteConfig`
// clones share one `SqliteQuickCheckManager` (upstream 3620b2caf8 / #49701); a
// freshly constructed config validates the same files independently.
type SqliteConfig struct {
	sqliteHome string
	quickCheck *sqliteQuickCheckManager
	// recoveryCollector receives the backups taken when a damaged database is
	// rebuilt (Rust `collect_runtime_db_backups`, upstream 3620b2caf8 / #49701).
	recoveryCollector *DBRecoveryCollector
	// corruptionMetrics receives confirmed `PRAGMA quick_check(1)` findings.
	// Rust threads a `telemetry_override` into `open_read_write_pool_with_spec`
	// and calls `record_corruption` for both recovery branches (upstream
	// 3620b2caf8 / #49701, `codex.sqlite.corruption.count`).
	corruptionMetrics *TaskMetrics
	// reclamationMetrics receives one `codex.sqlite.reclamation.*` sample per
	// background reclamation pass (Rust #49069, upstream 33a0f766a6).
	reclamationMetrics *TaskMetrics
	// filesystem is the probe shared with every copy of this config; a pass skips
	// a home on NFS (Rust #54417 `9c84e89c`).
	filesystem *sqliteFilesystemProbe
}

// WithRecoveryCollector returns a copy that reports the backups taken while
// recovering damaged runtime databases through the collector.
func (c SqliteConfig) WithRecoveryCollector(collector *DBRecoveryCollector) SqliteConfig {
	c.recoveryCollector = collector
	return c
}

// WithCorruptionMetrics returns a copy whose confirmed corruption findings are
// recorded through metrics. A nil sink records nothing, matching callers that
// have no telemetry sink installed.
func (c SqliteConfig) WithCorruptionMetrics(metrics *TaskMetrics) SqliteConfig {
	c.corruptionMetrics = metrics
	return c
}

// WithReclamationMetrics returns a copy whose background reclamation passes are
// recorded through metrics. A nil sink records nothing, matching the worker of a
// runtime that has no telemetry installed.
func (c SqliteConfig) WithReclamationMetrics(metrics *TaskMetrics) SqliteConfig {
	c.reclamationMetrics = metrics
	return c
}

func NewSqliteConfig(sqliteHome string) (SqliteConfig, error) {
	return sqliteConfigWithFilesystemProbe(sqliteHome, isNFSPath)
}

// sqliteConfigWithFilesystemProbe resolves a home with an explicit filesystem
// probe. Production passes the platform `isNFSPath`; tests drive the NFS
// behavior and the shared-probe contract without a Linux mount.
func sqliteConfigWithFilesystemProbe(sqliteHome string, probe func(path string) (bool, error)) (SqliteConfig, error) {
	sqliteHome = strings.TrimSpace(sqliteHome)
	if sqliteHome == "" {
		return SqliteConfig{}, fmt.Errorf("sqlite home is required")
	}
	absolute, err := filepath.Abs(sqliteHome)
	if err != nil {
		return SqliteConfig{}, fmt.Errorf("resolve sqlite home: %w", err)
	}
	return SqliteConfig{
		sqliteHome: filepath.Clean(absolute),
		quickCheck: &sqliteQuickCheckManager{},
		filesystem: newSqliteFilesystemProbe(probe),
	}, nil
}

// IsNFS reports whether the sqlite home sits on an NFS mount (Rust
// `SqliteConfig::is_nfs`, #54417 `9c84e89c`). The probe result is shared by
// every copy of the config that resolved the same home.
func (c SqliteConfig) IsNFS() (bool, error) {
	return c.filesystem.isNFS(c.sqliteHome)
}

func SqliteConfigForCodexHome(codexHome string) (SqliteConfig, error) {
	return NewSqliteConfig(ResolveSQLiteHome(codexHome))
}

// SqliteConfigForCodexHomeWithOverride resolves the SQLite home honoring the
// configured sqlite_home override first (Rust's managed config value wins),
// then the CODEX_SQLITE_HOME environment variable, then the codex home.
func SqliteConfigForCodexHomeWithOverride(codexHome, override string) (SqliteConfig, error) {
	override = strings.TrimSpace(override)
	if override != "" {
		return NewSqliteConfig(override)
	}
	return SqliteConfigForCodexHome(codexHome)
}

func ResolveSQLiteHome(codexHome string) string {
	if sqliteHome := strings.TrimSpace(os.Getenv("CODEX_SQLITE_HOME")); sqliteHome != "" {
		return sqliteHome
	}
	return strings.TrimSpace(codexHome)
}

func (c SqliteConfig) Home() string {
	return c.sqliteHome
}

func (c SqliteConfig) StateDBPath() string {
	return filepath.Join(c.sqliteHome, StateSQLiteFilename)
}

func (c SqliteConfig) LogsDBPath() string {
	return filepath.Join(c.sqliteHome, LogsSQLiteFilename)
}

func (c SqliteConfig) GoalsDBPath() string {
	return filepath.Join(c.sqliteHome, GoalsSQLiteFilename)
}

func (c SqliteConfig) MemoriesDBPath() string {
	return filepath.Join(c.sqliteHome, MemoriesSQLiteFilename)
}

// MemoriesV2DBPath is the isolated v2 memories database path (Rust #43797).
func (c SqliteConfig) MemoriesV2DBPath() string {
	return filepath.Join(c.sqliteHome, MemoriesV2SQLiteFilename)
}

func (c SqliteConfig) ThreadHistoryDBPath() string {
	return filepath.Join(c.sqliteHome, ThreadHistorySQLiteFilename)
}

func (c SqliteConfig) RuntimeDBPaths() []RuntimeDBPath {
	paths := make([]RuntimeDBPath, 0, len(runtimeDBSpecs)+1)
	for _, spec := range runtimeDBSpecs {
		paths = append(paths, RuntimeDBPath{
			Label:                 spec.label,
			Path:                  spec.path(c),
			BackgroundReclamation: spec.backgroundReclamation,
		})
	}
	// Thread history is opened lazily and never opts into reclamation.
	paths = append(paths, RuntimeDBPath{Label: "thread history DB", Path: c.ThreadHistoryDBPath()})
	return paths
}

func OpenSQLite(ctx context.Context, dataSourceName string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", dataSourceName)
	if err != nil {
		return nil, err
	}
	if err := RequireSQLiteVersion(ctx, db); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

// writableSQLiteConnector opens the connections of a writable Rust-compatible
// pool. Rust configures them with the pool's `after_connect` hook
// (codex-rs/state/src/sqlite.rs:364, #54389 `1ef30bcb`), which Go has no pool
// equivalent for, so the hook is applied to each connection the driver opens.
type writableSQLiteConnector struct {
	driver *modernsqlite.Driver
	dsn    string
}

func newWritableSQLiteConnector(dsn string) writableSQLiteConnector {
	return writableSQLiteConnector{driver: &modernsqlite.Driver{}, dsn: dsn}
}

func (c writableSQLiteConnector) Connect(ctx context.Context) (driver.Conn, error) {
	conn, err := c.driver.Open(c.dsn)
	if err != nil {
		return nil, err
	}
	if err := initializeConnectionSettings(ctx, conn); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("sqlite connection hook: %w", err)
	}
	return conn, nil
}

func (c writableSQLiteConnector) Driver() driver.Driver { return c.driver }

// initializeConnectionSettings is Rust's per-connection `after_connect`
// (#54389): a connection that finds the preserved DELETE journal mode raises
// `synchronous` to EXTRA, while every other mode keeps the DSN's
// `synchronous(NORMAL)`. Connections that open later need only settings that
// apply to each connection, because the journal and vacuum modes are already
// persisted on disk.
func initializeConnectionSettings(ctx context.Context, conn driver.Conn) error {
	queryer, ok := conn.(driver.QueryerContext)
	if !ok {
		return fmt.Errorf("sqlite connection does not support contextual queries")
	}
	rows, err := queryer.QueryContext(ctx, `PRAGMA journal_mode`, nil)
	if err != nil {
		return err
	}
	defer rows.Close()
	journal := ""
	dest := make([]driver.Value, len(rows.Columns()))
	for {
		if err := rows.Next(dest); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return err
		}
		if len(dest) == 0 {
			continue
		}
		if text, ok := dest[0].(string); ok {
			journal = strings.ToLower(strings.TrimSpace(text))
		}
	}
	if journal != sqliteJournalModeDelete {
		return nil
	}
	execer, ok := conn.(driver.ExecerContext)
	if !ok {
		return fmt.Errorf("sqlite connection does not support contextual exec")
	}
	// If the filesystem honors sync requests, EXTRA syncs the directory after
	// SQLite deletes a rollback journal.
	_, err = execer.ExecContext(ctx, `PRAGMA synchronous = EXTRA`, nil)
	return err
}

// OpenReadWrite opens a Rust-compatible writable SQLite pool. Connection-scoped
// PRAGMAs stay in the DSN so modernc applies them to every pooled connection;
// the journal and vacuum modes are database-scoped, so they are initialized once
// by the opener and otherwise preserved (#54389 `1ef30bcb`).
func (c SqliteConfig) OpenReadWrite(ctx context.Context, path string) (*sql.DB, error) {
	dsn, err := sqliteFileDSN(path, false)
	if err != nil {
		return nil, err
	}
	// Rust initializes the writable pool with `after_connect`, so the opener owns
	// the first initialization error instead of a pooled retry hiding it (#49102,
	// upstream c2d2f422e6), and retries a busy opener before that error surfaces
	// (#54369 `7ae8319917`). Each failed attempt closes its pool, which releases
	// the connection and its locks before the next attempt.
	return retryOnSQLiteBusy(func() (*sql.DB, error) {
		db := sql.OpenDB(newWritableSQLiteConnector(dsn))
		if err := RequireSQLiteVersion(ctx, db); err != nil {
			_ = db.Close()
			return nil, err
		}
		if err := initializeDatabaseSettings(ctx, db); err != nil {
			_ = db.Close()
			return nil, err
		}
		db.SetMaxOpenConns(SQLiteMaxOpenConnections)
		db.SetMaxIdleConns(SQLiteMaxOpenConnections)
		return db, nil
	})
}

// initializeDatabaseSettings applies the database-scoped PRAGMAs the Rust opener
// applies once before the pool is used (#54389 `1ef30bcb`):
//
//   - a database without a schema is initialized with incremental auto-vacuum and
//     WAL, including a file an interrupted initialization left with a header but
//     no schema;
//   - the journal and vacuum modes of an initialized database are preserved, so
//     opening a DELETE database never takes the writer lock;
//   - `synchronous` follows the journal mode the connection ended up with, since
//     another process can initialize WAL after this connection selected EXTRA.
func initializeDatabaseSettings(ctx context.Context, db *sql.DB) error {
	if db == nil {
		return fmt.Errorf("sqlite database is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	conn, err := db.Conn(ctx)
	if err != nil {
		return err
	}
	// Wait for the connection to return to the pool before startup requests it
	// again; an asynchronous drop can otherwise open a second connection.
	defer conn.Close()
	// Auto-vacuum alone can write a header to disk, so the schema check has to
	// decide whether the file is initialized, not whether the file is empty.
	var empty int64
	if err := conn.QueryRowContext(ctx, `SELECT NOT EXISTS (SELECT 1 FROM sqlite_schema)`).Scan(&empty); err != nil {
		return err
	}
	if empty == 1 {
		var mode int64
		if err := conn.QueryRowContext(ctx, `PRAGMA auto_vacuum`).Scan(&mode); err != nil {
			return err
		}
		// The setter takes the writer lock even when the mode is unchanged, so an
		// existing mode (including FULL) is preserved without taking it.
		if mode == 0 {
			if _, err := conn.ExecContext(ctx, `PRAGMA auto_vacuum = INCREMENTAL`); err != nil {
				return err
			}
		}
		if _, err := conn.ExecContext(ctx, `PRAGMA journal_mode = WAL`); err != nil {
			return err
		}
	}
	var journal string
	if err := conn.QueryRowContext(ctx, `PRAGMA journal_mode`).Scan(&journal); err != nil {
		return err
	}
	if strings.EqualFold(strings.TrimSpace(journal), sqliteJournalModeWAL) {
		if _, err := conn.ExecContext(ctx, `PRAGMA synchronous = NORMAL`); err != nil {
			return err
		}
	}
	return nil
}

// OpenReadOnly opens an existing database without creating or modifying it.
func (c SqliteConfig) OpenReadOnly(ctx context.Context, path string) (*sql.DB, error) {
	dsn, err := sqliteFileDSN(path, true)
	if err != nil {
		return nil, err
	}
	db, err := OpenSQLite(ctx, dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	return db, nil
}

func sqliteFileDSN(path string, readOnly bool) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", fmt.Errorf("sqlite database path is required")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve sqlite database path: %w", err)
	}
	uriPath := filepath.ToSlash(absolute)
	if runtime.GOOS == "windows" && !strings.HasPrefix(uriPath, "/") {
		uriPath = "/" + uriPath
	}
	u := &url.URL{Scheme: "file", Path: uriPath}
	query := u.Query()
	if readOnly {
		query.Set("mode", "ro")
	} else {
		// Connection-scoped PRAGMAs only: `journal_mode` and `auto_vacuum` are
		// database-scoped and are initialized by initializeDatabaseSettings so an
		// existing database keeps its journal and vacuum modes without taking the
		// writer lock (#49102, #54389). `synchronous(NORMAL)` is the pool default;
		// the connection hook raises it to EXTRA for a preserved DELETE journal.
		query.Add("_pragma", "busy_timeout(5000)")
		query.Add("_pragma", "synchronous(NORMAL)")
		query.Add("_pragma", "foreign_keys(ON)")
	}
	u.RawQuery = query.Encode()
	return u.String(), nil
}

func RequireSQLiteVersion(ctx context.Context, db *sql.DB) error {
	if db == nil {
		return fmt.Errorf("sqlite database is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	var version string
	if err := db.QueryRowContext(ctx, `SELECT sqlite_version()`).Scan(&version); err != nil {
		return fmt.Errorf("query sqlite version: %w", err)
	}
	ok, err := SQLiteVersionAtLeast(version, MinimumSQLiteVersion)
	if err != nil {
		return fmt.Errorf("validate sqlite version %q: %w", version, err)
	}
	if !ok {
		return fmt.Errorf("sqlite %s is unsupported; codex requires >= %s for the WAL-reset corruption fix", version, MinimumSQLiteVersion)
	}
	return nil
}

func SQLiteVersionAtLeast(version string, minimum string) (bool, error) {
	got, err := parseSQLiteVersion(version)
	if err != nil {
		return false, err
	}
	want, err := parseSQLiteVersion(minimum)
	if err != nil {
		return false, fmt.Errorf("invalid minimum version: %w", err)
	}
	for i := range got {
		if got[i] != want[i] {
			return got[i] > want[i], nil
		}
	}
	return true, nil
}

func parseSQLiteVersion(version string) ([3]int, error) {
	var parsed [3]int
	parts := strings.Split(strings.TrimSpace(version), ".")
	if len(parts) != len(parsed) {
		return parsed, fmt.Errorf("expected major.minor.patch, got %q", version)
	}
	for i, part := range parts {
		value, err := strconv.Atoi(part)
		if err != nil || value < 0 {
			return parsed, fmt.Errorf("invalid numeric component %q in %q", part, version)
		}
		parsed[i] = value
	}
	return parsed, nil
}
