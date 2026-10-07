package state

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"
)

// Mirrors Rust logs_maintenance_tests.rs (upstream 8ea2c0e0d4 / #49425).
const (
	logMaintenanceNow int64 = 2_000_000_000
	logMaintenanceDay int64 = 24 * 60 * 60
)

// logsDBWithAges creates a migrated logs database holding one 128 KiB payload
// per age (in seconds before logMaintenanceNow).
func logsDBWithAges(t *testing.T, ages []int64) *sql.DB {
	t.Helper()
	ctx := context.Background()
	config, err := NewSqliteConfig(t.TempDir())
	if err != nil {
		t.Fatalf("NewSqliteConfig: %v", err)
	}
	db, err := config.OpenReadWrite(ctx, config.LogsDBPath())
	if err != nil {
		t.Fatalf("OpenReadWrite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := migrateRuntimeDB(ctx, db, RuntimeDBLogs); err != nil {
		t.Fatalf("migrate logs database: %v", err)
	}
	for _, age := range ages {
		if _, err := db.ExecContext(ctx,
			`INSERT INTO logs (ts, ts_nanos, level, target, feedback_log_body) VALUES (?, 0, 'INFO', 'retention-test', ?)`,
			logMaintenanceNow-age, strings.Repeat("x", 128*1024)); err != nil {
			t.Fatalf("insert log with age %d: %v", age, err)
		}
	}
	return db
}

func retainedLogAges(t *testing.T, db *sql.DB) []int64 {
	t.Helper()
	rows, err := db.Query(`SELECT ? - ts FROM logs ORDER BY ts DESC`, logMaintenanceNow)
	if err != nil {
		t.Fatalf("query retained ages: %v", err)
	}
	defer rows.Close()
	var ages []int64
	for rows.Next() {
		var age int64
		if err := rows.Scan(&age); err != nil {
			t.Fatalf("scan retained age: %v", err)
		}
		ages = append(ages, age)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate retained ages: %v", err)
	}
	return ages
}

func assertLogAges(t *testing.T, db *sql.DB, want []int64) {
	t.Helper()
	got := retainedLogAges(t, db)
	if len(got) != len(want) {
		t.Fatalf("retained ages = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("retained ages = %v, want %v", got, want)
		}
	}
}

// Mirrors Rust keeps_ten_day_boundary_when_under_budget.
func TestPruneLogsByAgeAndSizeKeepsTenDayBoundaryLikeRust(t *testing.T) {
	db := logsDBWithAges(t, []int64{0, logRetentionSeconds, logRetentionSeconds + 1})
	if err := pruneLogsByAgeAndSize(context.Background(), db, logMaintenanceNow, 1024*1024); err != nil {
		t.Fatalf("prune: %v", err)
	}
	assertLogAges(t, db, []int64{0, logRetentionSeconds})
}

// Mirrors Rust halves_again_when_first_halving_deletes_nothing: the window keeps
// halving while the occupied pages exceed the budget, and free pages left behind
// do not count towards it.
func TestPruneLogsByAgeAndSizeHalvesAgainWhenFirstHalvingDeletesNothingLikeRust(t *testing.T) {
	db := logsDBWithAges(t, []int64{logMaintenanceDay, 2 * logMaintenanceDay, 4 * logMaintenanceDay})
	const budget = 350 * 1024
	if err := pruneLogsByAgeAndSize(context.Background(), db, logMaintenanceNow, budget); err != nil {
		t.Fatalf("prune: %v", err)
	}
	assertLogAges(t, db, []int64{logMaintenanceDay, 2 * logMaintenanceDay})
	var allocated int64
	if err := db.QueryRow(`SELECT page_count * page_size FROM pragma_page_count(), pragma_page_size()`).Scan(&allocated); err != nil {
		t.Fatalf("measure allocated pages: %v", err)
	}
	if allocated <= budget {
		t.Fatalf("allocated bytes = %d, want the file to keep free pages above %d", allocated, budget)
	}
	// A second cleanup must ignore the free pages it just released.
	if err := pruneLogsByAgeAndSize(context.Background(), db, logMaintenanceNow, budget); err != nil {
		t.Fatalf("second prune: %v", err)
	}
	assertLogAges(t, db, []int64{logMaintenanceDay, 2 * logMaintenanceDay})
}

// Mirrors Rust stops_at_one_second_when_recent_rows_exceed_budget.
func TestPruneLogsByAgeAndSizeStopsAtOneSecondWhenRecentRowsExceedBudgetLikeRust(t *testing.T) {
	db := logsDBWithAges(t, []int64{-1, 0, 1, 2, logMaintenanceDay})
	if err := pruneLogsByAgeAndSize(context.Background(), db, logMaintenanceNow, 1); err != nil {
		t.Fatalf("prune: %v", err)
	}
	assertLogAges(t, db, []int64{-1, 0, 1})
}

// Mirrors Rust startup_cleanup_runs_without_waiting_for_the_period: the sweep
// runs immediately when the runtime initializes instead of only on the timer.
func TestStartupLogsCleanupRunsWithoutWaitingForThePeriodLikeRust(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	config, err := NewSqliteConfig(home)
	if err != nil {
		t.Fatalf("NewSqliteConfig: %v", err)
	}
	db, err := config.OpenReadWrite(ctx, config.LogsDBPath())
	if err != nil {
		t.Fatalf("OpenReadWrite: %v", err)
	}
	if err := migrateRuntimeDB(ctx, db, RuntimeDBLogs); err != nil {
		t.Fatalf("migrate logs database: %v", err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO logs (ts, ts_nanos, level, target) VALUES (0, 0, 'INFO', 'test')`); err != nil {
		t.Fatalf("insert expired log: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close logs database: %v", err)
	}

	runtime, err := InitStateRuntime(ctx, config, "openai")
	if err != nil {
		t.Fatalf("InitStateRuntime: %v", err)
	}
	defer runtime.Close()
	waitForLogRows(t, runtime, func(count int) bool { return count == 0 })
}

// Mirrors Rust periodic_cleanup_prunes_without_new_log_writes_or_restart.
func TestPeriodicLogsCleanupPrunesWithoutNewWritesOrRestartLikeRust(t *testing.T) {
	ctx := context.Background()
	runtime, err := InitStateRuntime(ctx, mustSqliteConfig(t, t.TempDir()), "openai")
	if err != nil {
		t.Fatalf("InitStateRuntime: %v", err)
	}
	defer runtime.Close()
	now := time.Now().Unix()
	fresh := func() {
		t.Helper()
		if _, err := runtime.logsDB.ExecContext(ctx, `INSERT INTO logs (ts, ts_nanos, level, target) VALUES (?, 0, 'INFO', 'test')`, now); err != nil {
			t.Fatalf("insert fresh log: %v", err)
		}
	}
	expired := func() {
		t.Helper()
		if _, err := runtime.logsDB.ExecContext(ctx, `INSERT INTO logs (ts, ts_nanos, level, target) VALUES (?, 0, 'INFO', 'test')`, now-11*logMaintenanceDay); err != nil {
			t.Fatalf("insert expired log: %v", err)
		}
	}
	// The maintenance task sweeps as soon as it starts and then on the timer, so
	// pruning an idle runtime must not need new log writes or a restart. Each
	// round inserts an expired row only after the previous round was pruned: a
	// task that swept once and stopped can cover at most the first round, so the
	// remaining rounds can only be reached by the periodic re-sweep.
	fresh()
	expired()
	runtime.startPeriodicLogsMaintenance(10 * time.Millisecond)
	for round := 0; round < 3; round++ {
		waitForLogRows(t, runtime, func(count int) bool { return count == 1 })
		expired()
	}
	var ts int64
	if err := runtime.logsDB.QueryRowContext(ctx, `SELECT ts FROM logs`).Scan(&ts); err != nil {
		t.Fatalf("read surviving log: %v", err)
	}
	if ts != now {
		t.Fatalf("surviving log ts = %d, want %d", ts, now)
	}
}

func mustSqliteConfig(t *testing.T, home string) SqliteConfig {
	t.Helper()
	config, err := NewSqliteConfig(home)
	if err != nil {
		t.Fatalf("NewSqliteConfig: %v", err)
	}
	return config
}

// waitForLogRows polls the runtime's logs table until the predicate holds.
func waitForLogRows(t *testing.T, runtime *StateRuntime, done func(int) bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		var count int
		if err := runtime.logsDB.QueryRow(`SELECT COUNT(*) FROM logs`).Scan(&count); err != nil {
			t.Fatalf("count logs: %v", err)
		}
		if done(count) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("logs table still holds %d rows after 5s", count)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
