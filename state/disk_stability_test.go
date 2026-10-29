//go:build stability

package state

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"codex_go/stability/harness/disk"
	"codex_go/stability/harness/report"
)

// TestDiskWALCheckpoint writes a realistic log volume into the production logs
// database, checkpoints it, and proves two things: the WAL stays inside the
// budget and no rows are lost across a reopen.
func TestDiskWALCheckpoint(t *testing.T) {
	rows := envIntForDisk("CODEX_STABILITY_WAL_ROWS", 5000)
	root := t.TempDir()
	cfg, err := NewSqliteConfig(root)
	if err != nil {
		t.Fatalf("sqlite config: %v", err)
	}
	ctx := context.Background()

	db, err := cfg.OpenLogsDB(ctx)
	if err != nil {
		t.Fatalf("open logs db: %v", err)
	}
	if _, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS stability_probe (id INTEGER PRIMARY KEY, payload BLOB)`); err != nil {
		_ = db.Close()
		t.Fatalf("create probe table: %v", err)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		_ = db.Close()
		t.Fatalf("begin: %v", err)
	}
	statement, err := tx.PrepareContext(ctx, `INSERT INTO stability_probe (payload) VALUES (?)`)
	if err != nil {
		_ = tx.Rollback()
		_ = db.Close()
		t.Fatalf("prepare insert: %v", err)
	}
	payload := make([]byte, 1024)
	for index := range payload {
		payload[index] = 'w'
	}
	for i := 0; i < rows; i++ {
		if _, err := statement.ExecContext(ctx, payload); err != nil {
			_ = statement.Close()
			_ = tx.Rollback()
			_ = db.Close()
			t.Fatalf("insert %d: %v", i, err)
		}
	}
	if err := statement.Close(); err != nil {
		t.Fatalf("close statement: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}

	before, err := disk.Take(root)
	if err != nil {
		t.Fatalf("disk snapshot before checkpoint: %v", err)
	}
	// PASSIVE first so the measurement includes a bounded (non-truncating)
	// checkpoint, then TRUNCATE which must leave an empty WAL.
	if _, err := db.ExecContext(ctx, `PRAGMA wal_checkpoint(PASSIVE)`); err != nil {
		t.Fatalf("passive checkpoint: %v", err)
	}
	passive, err := disk.Take(root)
	if err != nil {
		t.Fatalf("disk snapshot after passive checkpoint: %v", err)
	}
	if _, err := db.ExecContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		t.Fatalf("truncate checkpoint: %v", err)
	}
	after, err := disk.Take(root)
	if err != nil {
		t.Fatalf("disk snapshot after truncate: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close logs db: %v", err)
	}

	// Reopen through the production config and confirm durability.
	reopened, err := cfg.OpenLogsDB(ctx)
	if err != nil {
		t.Fatalf("reopen logs db: %v", err)
	}
	var count int
	if err := reopened.QueryRowContext(ctx, `SELECT COUNT(*) FROM stability_probe`).Scan(&count); err != nil {
		_ = reopened.Close()
		t.Fatalf("count rows: %v", err)
	}
	if err := reopened.Close(); err != nil {
		t.Fatalf("close reopened db: %v", err)
	}

	budgets, err := report.LoadBudgets(budgetsPathForState(t))
	if err != nil {
		t.Fatalf("load budgets: %v", err)
	}
	run := report.New("disk-wal-checkpoint")
	run.Env["rows"] = strconv.Itoa(rows)
	run.Env["wal_before"] = strconv.FormatInt(before.WALBytes, 10)
	run.Env["wal_passive"] = strconv.FormatInt(passive.WALBytes, 10)
	run.Env["wal_after_truncate"] = strconv.FormatInt(after.WALBytes, 10)
	run.AddDisk(before)
	run.AddDisk(passive)
	run.AddDisk(after)

	run.Check("disk.wal_bytes", after.WALBytes <= budgets.Disk.WALBytes,
		"wal=%d budget=%d", after.WALBytes, budgets.Disk.WALBytes)
	run.Check("disk.wal_truncated", after.WALBytes <= 4096,
		"wal=%d after TRUNCATE checkpoint", after.WALBytes)
	run.Check("disk.wal_rows_survive_reopen", count == rows, "rows=%d want %d", count, rows)
	run.Check("disk.orphan_temp_files", after.TempFiles <= budgets.Disk.OrphanTempFiles,
		"temp_files=%d budget=%d", after.TempFiles, budgets.Disk.OrphanTempFiles)
	run.Metric("disk.wal_bytes", float64(after.WALBytes))
	run.Metric("disk.wal_rows", float64(count))
	run.Finish()

	path, err := run.Write(reportDirForState(t))
	if err != nil {
		t.Fatalf("write report: %v", err)
	}
	if !run.Passed {
		t.Fatalf("wal scenario failed: %+v (report %s)", run.Checks, path)
	}
}

func budgetsPathForState(t *testing.T) string {
	t.Helper()
	if path := os.Getenv("CODEX_STABILITY_BUDGETS"); path != "" {
		return path
	}
	return filepath.Join("..", "stability", "budgets.json")
}

func reportDirForState(t *testing.T) string {
	t.Helper()
	if path := os.Getenv("CODEX_STABILITY_REPORT_DIR"); path != "" {
		return path
	}
	return filepath.Join("..", "stability", "reports")
}

func envIntForDisk(name string, fallback int) int {
	value := os.Getenv(name)
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed <= 0 {
		return fallback
	}
	return parsed
}
