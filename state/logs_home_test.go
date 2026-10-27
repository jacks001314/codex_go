package state

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// Mirrors Rust `SqliteConfig::with_local_logs_home` / `legacy_logs_db_path`
// (upstream 17a0e6f63f, #55978).
func TestWithLocalLogsHomeRoutesOnlyLogsLikeRust(t *testing.T) {
	home := t.TempDir()
	logsHome := filepath.Join(t.TempDir(), "log")
	config := mustSqliteConfig(t, home).WithLocalLogsHome(logsHome)

	if got, want := config.LogsHome(), logsHome; got != want {
		t.Fatalf("LogsHome() = %q, want %q", got, want)
	}
	if got, want := config.LogsDBPath(), filepath.Join(logsHome, LogsSQLiteFilename); got != want {
		t.Fatalf("LogsDBPath() = %q, want %q", got, want)
	}
	if got, want := config.StateDBPath(), filepath.Join(home, StateSQLiteFilename); got != want {
		t.Fatalf("StateDBPath() = %q, want %q", got, want)
	}
	if got, want := config.GoalsDBPath(), filepath.Join(home, GoalsSQLiteFilename); got != want {
		t.Fatalf("GoalsDBPath() = %q, want %q", got, want)
	}
	legacy, ok := config.LegacyLogsDBPath()
	if !ok || legacy != filepath.Join(home, LogsSQLiteFilename) {
		t.Fatalf("LegacyLogsDBPath() = %q, %v", legacy, ok)
	}
	for _, database := range config.RuntimeDBPaths() {
		if database.Label != "logs DB" {
			continue
		}
		if database.Path != config.LogsDBPath() {
			t.Fatalf("runtime logs path = %q, want %q", database.Path, config.LogsDBPath())
		}
	}
	var legacyReclamation bool
	for _, database := range config.ReclamationDBPaths() {
		if database.Label == "legacy log DB" && database.Path == legacy && database.BackgroundReclamation {
			legacyReclamation = true
		}
	}
	if !legacyReclamation {
		t.Fatalf("reclamation paths = %#v, want the legacy log DB", config.ReclamationDBPaths())
	}

	// Without an explicit logs home there is no legacy location to read.
	plain := mustSqliteConfig(t, home)
	if legacy, ok := plain.LegacyLogsDBPath(); ok || legacy != "" {
		t.Fatalf("LegacyLogsDBPath() without logs home = %q, %v", legacy, ok)
	}
	if plain.LogsDBPath() != filepath.Join(home, LogsSQLiteFilename) {
		t.Fatalf("default logs path = %q", plain.LogsDBPath())
	}
}

// The configured logs directory is created only when its database opens, so an
// unavailable log_dir does not break configuration reads (Rust #55978).
func TestLocalLogsDirectoryIsCreatedLazilyLikeRust(t *testing.T) {
	home := t.TempDir()
	logsHome := filepath.Join(t.TempDir(), "nested", "log")
	config := mustSqliteConfig(t, home).WithLocalLogsHome(logsHome)
	if _, err := os.Stat(logsHome); !os.IsNotExist(err) {
		t.Fatalf("config construction touched the logs directory: %v", err)
	}

	ctx := context.Background()
	db, err := config.OpenReadWrite(ctx, config.LogsDBPath())
	if err != nil {
		t.Fatalf("OpenReadWrite: %v", err)
	}
	_ = db.Close()
	info, err := os.Stat(logsHome)
	if err != nil || !info.IsDir() {
		t.Fatalf("logs directory was not created: %v", err)
	}

	// A path that cannot be a directory fails only when the logs database opens.
	blocked := filepath.Join(t.TempDir(), "blocked")
	if err := os.WriteFile(blocked, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	blockedConfig := mustSqliteConfig(t, home).WithLocalLogsHome(blocked)
	if _, err := blockedConfig.OpenReadWrite(ctx, blockedConfig.LogsDBPath()); err == nil {
		t.Fatalf("opening logs under an unusable log_dir must fail")
	}
}

func TestLegacyFeedbackLogsFallbackLikeRust(t *testing.T) {
	home := t.TempDir()
	ctx := context.Background()
	threadID := "legacy-thread"
	body := "legacy diagnostic log"

	// Seed the pre-#55978 location in the SQLite home.
	legacyConfig := mustSqliteConfig(t, home)
	legacyDB, err := legacyConfig.OpenReadWrite(ctx, legacyConfig.LogsDBPath())
	if err != nil {
		t.Fatalf("open legacy logs: %v", err)
	}
	if err := migrateRuntimeDB(ctx, legacyDB, RuntimeDBLogs); err != nil {
		t.Fatalf("migrate legacy logs: %v", err)
	}
	if _, err := legacyDB.ExecContext(ctx,
		`INSERT INTO logs (ts, ts_nanos, level, target, feedback_log_body, thread_id) VALUES (?, 0, 'INFO', 'legacy', ?, ?)`,
		time.Now().Unix(), body, threadID); err != nil {
		t.Fatalf("insert legacy log: %v", err)
	}
	_ = legacyDB.Close()

	logsHome := filepath.Join(t.TempDir(), "log")
	runtime, err := InitStateRuntime(ctx, legacyConfig.WithLocalLogsHome(logsHome), "test-provider")
	if err != nil {
		t.Fatalf("InitStateRuntime: %v", err)
	}
	t.Cleanup(func() { _ = runtime.Close() })

	if current, err := runtime.QueryFeedbackLogsForThreads(ctx, []string{threadID}); err != nil || len(current) != 0 {
		t.Fatalf("current logs = %q, %v; want none", current, err)
	}
	legacy, err := runtime.QueryLegacyFeedbackLogsForThreads(ctx, []string{threadID})
	if err != nil {
		t.Fatalf("QueryLegacyFeedbackLogsForThreads: %v", err)
	}
	if !strings.Contains(string(legacy), body) {
		t.Fatalf("legacy logs = %q, want %q", legacy, body)
	}

	// Without an explicit logs home the runtime reads the same database as the
	// current one and the legacy query is empty.
	plain := newGoalTestRuntime(t)
	if legacy, err := plain.QueryLegacyFeedbackLogsForThreads(ctx, []string{threadID}); err != nil || len(legacy) != 0 {
		t.Fatalf("legacy logs without logs home = %q, %v", legacy, err)
	}
}

// Background maintenance prunes both the current and the legacy database and
// never creates a missing legacy one (Rust #55978).
func TestLogsMaintenancePrunesLegacyDatabaseLikeRust(t *testing.T) {
	home := t.TempDir()
	ctx := context.Background()
	legacyConfig := mustSqliteConfig(t, home)
	legacyDB, err := legacyConfig.OpenReadWrite(ctx, legacyConfig.LogsDBPath())
	if err != nil {
		t.Fatalf("open legacy logs: %v", err)
	}
	if err := migrateRuntimeDB(ctx, legacyDB, RuntimeDBLogs); err != nil {
		t.Fatalf("migrate legacy logs: %v", err)
	}
	now := time.Now().Unix()
	for _, age := range []int64{11 * 24 * 60 * 60, 60} {
		if _, err := legacyDB.ExecContext(ctx,
			`INSERT INTO logs (ts, ts_nanos, level, target, feedback_log_body) VALUES (?, 0, 'INFO', 'legacy', 'row')`,
			now-age); err != nil {
			t.Fatalf("insert legacy log: %v", err)
		}
	}
	// Close the writer so the runtime's maintenance connection is not blocked.
	if err := legacyDB.Close(); err != nil {
		t.Fatalf("close legacy logs: %v", err)
	}

	logsHome := filepath.Join(t.TempDir(), "log")
	config := legacyConfig.WithLocalLogsHome(logsHome)
	currentDB, err := config.OpenReadWrite(ctx, config.LogsDBPath())
	if err != nil {
		t.Fatalf("open current logs: %v", err)
	}
	if err := migrateRuntimeDB(ctx, currentDB, RuntimeDBLogs); err != nil {
		t.Fatalf("migrate current logs: %v", err)
	}
	t.Cleanup(func() { _ = currentDB.Close() })
	// Drive the sweep directly so the assertion does not race the background
	// worker that a full runtime would start.
	runtime := &StateRuntime{sqlite: config, logsDB: currentDB}
	runtime.pruneDiagnosticLogs(ctx, true)

	reader, err := legacyConfig.OpenReadOnly(ctx, filepath.Join(home, LogsSQLiteFilename))
	if err != nil {
		t.Fatalf("open legacy logs read-only: %v", err)
	}
	defer reader.Close()
	var count int
	if err := reader.QueryRowContext(ctx, `SELECT COUNT(*) FROM logs`).Scan(&count); err != nil {
		t.Fatalf("count legacy logs: %v", err)
	}
	if count != 1 {
		t.Fatalf("legacy logs retained %d rows, want only the fresh row", count)
	}

	// A missing legacy database is never created by maintenance.
	emptyHome := t.TempDir()
	emptyConfig := mustSqliteConfig(t, emptyHome).WithLocalLogsHome(filepath.Join(t.TempDir(), "log"))
	emptyDB, err := emptyConfig.OpenReadWrite(ctx, emptyConfig.LogsDBPath())
	if err != nil {
		t.Fatalf("open current logs: %v", err)
	}
	t.Cleanup(func() { _ = emptyDB.Close() })
	if err := migrateRuntimeDB(ctx, emptyDB, RuntimeDBLogs); err != nil {
		t.Fatalf("migrate current logs: %v", err)
	}
	emptyRuntime := &StateRuntime{sqlite: emptyConfig, logsDB: emptyDB}
	emptyRuntime.pruneDiagnosticLogs(ctx, true)
	if _, err := os.Stat(filepath.Join(emptyHome, LogsSQLiteFilename)); !os.IsNotExist(err) {
		t.Fatalf("maintenance created a legacy logs database: %v", err)
	}
}

// Current and legacy log directories each keep their own maintenance lock, so
// holding the current lock does not stop legacy reclamation (Rust #55978).
func TestReclamationUsesPerDirectoryLocksLikeRust(t *testing.T) {
	home := t.TempDir()
	logsHome := filepath.Join(t.TempDir(), "log")
	config := mustSqliteConfig(t, home).WithLocalLogsHome(logsHome)
	if err := os.MkdirAll(logsHome, 0o700); err != nil {
		t.Fatal(err)
	}
	worker := newSqliteReclamationWorker(config)
	if worker == nil {
		t.Fatalf("the logs databases must opt into background reclamation")
	}
	if len(worker.targets) != 2 {
		t.Fatalf("reclamation targets = %#v, want current and legacy logs", worker.targets)
	}
	owner, err := tryReclamationOwnership(filepath.Dir(config.LogsDBPath()))
	if err != nil || owner == nil {
		t.Fatalf("take the current logs lock: %v", err)
	}
	t.Cleanup(func() { _ = owner.Unlock() })

	var currentPasses, legacyPasses atomic.Int64
	worker.idleInterval = time.Millisecond
	worker.probe = func(_ context.Context, target reclamationTarget, _ reclamationOptions, _ <-chan struct{}) reclamationResult {
		if target.path == config.LogsDBPath() {
			currentPasses.Add(1)
		} else {
			legacyPasses.Add(1)
		}
		return reclamationResult{pass: reclamationPass{outcome: reclamationOutcomeActive}}
	}
	go worker.run()
	t.Cleanup(worker.close)
	waitForReclamation(t, func() bool { return legacyPasses.Load() > 0 }, "the legacy logs database to be reclaimed")
	if got := currentPasses.Load(); got != 0 {
		t.Fatalf("current logs ran %d passes while another worker held its directory lock", got)
	}
}
