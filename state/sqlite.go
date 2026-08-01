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
	MinimumSQLiteVersion        = "3.51.3"
	StateSQLiteFilename         = "state_5.sqlite"
	LogsSQLiteFilename          = "logs_2.sqlite"
	GoalsSQLiteFilename         = "goals_1.sqlite"
	MemoriesSQLiteFilename      = "memories_1.sqlite"
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
}

// SqliteConfig is the single resolved home used by every Codex runtime DB.
type SqliteConfig struct {
	sqliteHome string
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
	return SqliteConfig{sqliteHome: filepath.Clean(absolute)}, nil
}

func SqliteConfigForCodexHome(codexHome string) (SqliteConfig, error) {
	return NewSqliteConfig(ResolveSQLiteHome(codexHome))
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

func (c SqliteConfig) ThreadHistoryDBPath() string {
	return filepath.Join(c.sqliteHome, ThreadHistorySQLiteFilename)
}

func (c SqliteConfig) RuntimeDBPaths() []RuntimeDBPath {
	return []RuntimeDBPath{
		{Label: "state DB", Path: c.StateDBPath()},
		{Label: "log DB", Path: c.LogsDBPath()},
		{Label: "goals DB", Path: c.GoalsDBPath()},
		{Label: "memories DB", Path: c.MemoriesDBPath()},
		{Label: "thread history DB", Path: c.ThreadHistoryDBPath()},
	}
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

// OpenReadWrite opens a Rust-compatible writable SQLite pool. PRAGMAs are
// encoded in the DSN so modernc applies them to every pooled connection.
func (c SqliteConfig) OpenReadWrite(ctx context.Context, path string) (*sql.DB, error) {
	dsn, err := sqliteFileDSN(path, false)
	if err != nil {
		return nil, err
	}
	db, err := OpenSQLite(ctx, dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(SQLiteMaxOpenConnections)
	db.SetMaxIdleConns(SQLiteMaxOpenConnections)
	return db, nil
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
		query.Add("_pragma", "busy_timeout(5000)")
		query.Add("_pragma", "journal_mode(WAL)")
		query.Add("_pragma", "synchronous(NORMAL)")
		query.Add("_pragma", "auto_vacuum(INCREMENTAL)")
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
