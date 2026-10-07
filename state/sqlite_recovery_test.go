package state

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Mirrors Rust runtime_opens_recover_or_report_corruption_by_database_policy
// (codex-rs/state/src/sqlite/validation_tests.rs, upstream 3620b2caf8 / #49701):
// every runtime database with an automatic recovery policy is backed up and
// rebuilt inside the opener, and thread history is report-only.
func TestRuntimeRecoversCorruptionBySpecPolicyLikeRust(t *testing.T) {
	ctx := context.Background()
	recoverable := []struct {
		label string
		path  func(SqliteConfig) string
	}{
		{"state DB", func(c SqliteConfig) string { return c.StateDBPath() }},
		{"log DB", func(c SqliteConfig) string { return c.LogsDBPath() }},
		{"goals DB", func(c SqliteConfig) string { return c.GoalsDBPath() }},
		{"memories DB", func(c SqliteConfig) string { return c.MemoriesDBPath() }},
	}
	for _, tc := range recoverable {
		t.Run(tc.label, func(t *testing.T) {
			config, err := NewSqliteConfig(t.TempDir())
			if err != nil {
				t.Fatalf("NewSqliteConfig: %v", err)
			}
			databasePath := tc.path(config)
			corruptValidationDB(t, databasePath)

			// Recovery happens inside the database opener, without a CLI or app
			// server; a second init must find a healthy database.
			for attempt := 0; attempt < 2; attempt++ {
				runtime, err := InitStateRuntime(ctx, config, "openai")
				if err != nil {
					t.Fatalf("InitStateRuntime attempt %d: %v", attempt, err)
				}
				if err := runtime.Close(); err != nil {
					t.Fatalf("close runtime attempt %d: %v", attempt, err)
				}
			}

			assertBackupPreservesCorruption(t, config, databasePath)
			// The rebuilt database is healthy: a later open reports no finding.
			assertQuickCheckFinding(t, config, databasePath, "ok")
		})
	}

	t.Run("memories v2 DB", func(t *testing.T) {
		config, err := NewSqliteConfig(t.TempDir())
		if err != nil {
			t.Fatalf("NewSqliteConfig: %v", err)
		}
		databasePath := config.MemoriesV2DBPath()
		corruptValidationDB(t, databasePath)
		for attempt := 0; attempt < 2; attempt++ {
			db, err := config.OpenMemoriesV2DB(ctx)
			if err != nil {
				t.Fatalf("OpenMemoriesV2DB attempt %d: %v", attempt, err)
			}
			if got := quickCheck(ctx, db, testQuickCheckBudget); got != QuickCheckComplete {
				t.Fatalf("rebuilt memories v2 quick_check = %v, want %v", got, QuickCheckComplete)
			}
			if err := db.Close(); err != nil {
				t.Fatalf("close memories v2 attempt %d: %v", attempt, err)
			}
		}
		assertBackupPreservesCorruption(t, config, databasePath)
	})

	t.Run("thread history DB", func(t *testing.T) {
		config, err := NewSqliteConfig(t.TempDir())
		if err != nil {
			t.Fatalf("NewSqliteConfig: %v", err)
		}
		databasePath := config.ThreadHistoryDBPath()
		corruptValidationDB(t, databasePath)
		// Unrelated corruption must not disable lazy history reads when the
		// database has no automatic recovery policy. Exercise both migration and
		// reopening.
		for attempt := 0; attempt < 2; attempt++ {
			runtime, err := InitStateRuntime(ctx, config, "openai")
			if err != nil {
				t.Fatalf("InitStateRuntime attempt %d: %v", attempt, err)
			}
			db, err := runtime.ThreadHistoryDB(ctx)
			if err != nil {
				t.Fatalf("ThreadHistoryDB attempt %d: %v", attempt, err)
			}
			var count int
			if err := db.QueryRowContext(ctx, "SELECT count(*) FROM thread_turns").Scan(&count); err != nil {
				t.Fatalf("read thread turns attempt %d: %v", attempt, err)
			}
			if got := quickCheck(ctx, db, testQuickCheckBudget); got != QuickCheckCorruptedNeedsFixed {
				t.Fatalf("thread history quick_check attempt %d = %v, want %v", attempt, got, QuickCheckCorruptedNeedsFixed)
			}
			if err := runtime.Close(); err != nil {
				t.Fatalf("close runtime attempt %d: %v", attempt, err)
			}
		}
		if _, err := os.Stat(filepath.Join(config.Home(), dbRecoveryBackupDirName)); !os.IsNotExist(err) {
			t.Fatalf("thread history must not be rebuilt, stat err = %v", err)
		}
	})
}

// assertBackupPreservesCorruption checks that exactly one backup folder was
// created for the corrupted database and that the preserved copy still carries
// the original finding and data, exactly like the Rust assertions.
func assertBackupPreservesCorruption(t *testing.T, config SqliteConfig, databasePath string) {
	t.Helper()
	backupDir := filepath.Join(config.Home(), dbRecoveryBackupDirName)
	entries, err := os.ReadDir(backupDir)
	if err != nil {
		t.Fatalf("read backup dir %s: %v", backupDir, err)
	}
	if len(entries) != 1 {
		t.Fatalf("backup folders = %d, want 1", len(entries))
	}
	backupPath := filepath.Join(backupDir, entries[0].Name(), filepath.Base(databasePath))
	backup, err := config.OpenReadOnly(context.Background(), backupPath)
	if err != nil {
		t.Fatalf("open backup %s: %v", backupPath, err)
	}
	defer backup.Close()
	if got := quickCheckFinding(t, backup); got != "NULL value in sample.value" {
		t.Fatalf("backup quick_check = %q, want %q", got, "NULL value in sample.value")
	}
	if got := quickCheck(context.Background(), backup, testQuickCheckBudget); got != QuickCheckCorruptedNeedsFixed {
		t.Fatalf("backup outcome = %v, want %v", got, QuickCheckCorruptedNeedsFixed)
	}
	var tables []string
	rows, err := backup.Query("SELECT name FROM sqlite_schema WHERE type = 'table'")
	if err != nil {
		t.Fatalf("list backup tables: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scan backup table: %v", err)
		}
		tables = append(tables, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate backup tables: %v", err)
	}
	found := false
	for _, name := range tables {
		if name == "sample" {
			found = true
		}
	}
	if !found {
		t.Fatalf("backup tables = %v, want to contain %q", tables, "sample")
	}
}

// assertQuickCheckFinding opens the given database read-only and asserts the
// exact `PRAGMA quick_check(1)` row.
func assertQuickCheckFinding(t *testing.T, config SqliteConfig, path string, want string) {
	t.Helper()
	db, err := config.OpenReadOnly(context.Background(), path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer db.Close()
	if got := quickCheckFinding(t, db); got != want {
		t.Fatalf("quick_check(%s) = %q, want %q", path, got, want)
	}
}

func quickCheckFinding(t *testing.T, db *sql.DB) string {
	t.Helper()
	var result string
	if err := db.QueryRow("PRAGMA quick_check(1)").Scan(&result); err != nil {
		t.Fatalf("quick_check: %v", err)
	}
	return result
}

// testQuickCheckBudget mirrors the generous 5 s budget the upstream recovery
// test uses when it re-scans a database directly (Rust
// `Instant::now() + Duration::from_secs(5)`), decoupled from the 100 ms startup
// budget under test.
const testQuickCheckBudget = 5 * time.Second
