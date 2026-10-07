package state

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	_ "modernc.org/sqlite"
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

func NewSqliteConfig(sqliteHome string) (SqliteConfig, error) {
	sqliteHome = strings.TrimSpace(sqliteHome)
	if sqliteHome == "" {
		return SqliteConfig{}, fmt.Errorf("sqlite home is required")
	}
	absolute, err := filepath.Abs(sqliteHome)
	if err != nil {
		return SqliteConfig{}, fmt.Errorf("resolve sqlite home: %w", err)
	}
	return SqliteConfig{sqliteHome: filepath.Clean(absolute), quickCheck: &sqliteQuickCheckManager{}}, nil
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

// OpenReadWrite opens a Rust-compatible writable SQLite pool. Connection-scoped
// PRAGMAs stay in the DSN so modernc applies them to every pooled connection;
// the database-scoped ones are initialized once by the opener.
func (c SqliteConfig) OpenReadWrite(ctx context.Context, path string) (*sql.DB, error) {
	dsn, err := sqliteFileDSN(path, false)
	if err != nil {
		return nil, err
	}
	db, err := OpenSQLite(ctx, dsn)
	if err != nil {
		return nil, err
	}
	// Rust initializes the writable pool with `after_connect`, so the opener owns
	// the first initialization error instead of a pooled retry hiding it (#49102,
	// upstream c2d2f422e6). Go opens the settings once and returns that error
	// unchanged, then closes the pool so a later startup can succeed.
	if err := initializeDatabaseSettings(ctx, db); err != nil {
		_ = db.Close()
		return nil, err
	}
	db.SetMaxOpenConns(SQLiteMaxOpenConnections)
	db.SetMaxIdleConns(SQLiteMaxOpenConnections)
	return db, nil
}

// initializeDatabaseSettings applies the database-scoped PRAGMAs the Rust opener
// sets in `after_connect` (#49102): existing vacuum modes are preserved and only
// an empty database is initialized with incremental auto-vacuum, because the
// setter takes the writer lock even when the mode is unchanged.
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
	defer conn.Close()
	var mode int64
	if err := conn.QueryRowContext(ctx, `PRAGMA auto_vacuum`).Scan(&mode); err != nil {
		return err
	}
	if mode == 0 {
		var empty int64
		if err := conn.QueryRowContext(ctx, `SELECT NOT EXISTS (SELECT 1 FROM sqlite_schema)`).Scan(&empty); err != nil {
			return err
		}
		if empty == 1 {
			if _, err := conn.ExecContext(ctx, `PRAGMA auto_vacuum = INCREMENTAL`); err != nil {
				return err
			}
		}
	}
	// WAL is a database-scoped setting too, so the conversion runs once; a held
	// writer surfaces as a lock error from the opener (Rust #49102).
	if _, err := conn.ExecContext(ctx, `PRAGMA journal_mode = WAL`); err != nil {
		return err
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
		// existing database keeps its vacuum mode without taking the writer lock
		// (#49102).
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
