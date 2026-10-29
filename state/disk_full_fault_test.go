//go:build stability

package state

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"codex_go/stability/harness/disk"
	"codex_go/stability/harness/report"

	modernsqlite "modernc.org/sqlite"
)

// sqliteFullCode is SQLITE_FULL, the error SQLite returns when the database
// cannot grow. The scenario reaches it with PRAGMA max_page_count so a real
// out-of-space write failure is exercised without filling a volume.
const sqliteFullCode = 13

// TestFaultDiskFull drives the production logs database into a real
// out-of-space error, then proves the failure is diagnosable, leaves no orphan
// files, keeps committed rows readable, and does not permanently wedge the
// database once space is available again.
func TestFaultDiskFull(t *testing.T) {
	run := report.New("fault-disk-full")
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
	defer func() { _ = db.Close() }()
	// One connection keeps PRAGMA max_page_count in force for every statement
	// (PRAGMAs are per-connection).
	db.SetMaxOpenConns(1)

	if _, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS stability_disk_full (id INTEGER PRIMARY KEY, payload BLOB)`); err != nil {
		t.Fatalf("create probe table: %v", err)
	}
	var pageCount, pageSize int
	if err := db.QueryRowContext(ctx, `PRAGMA page_count`).Scan(&pageCount); err != nil {
		t.Fatalf("page_count: %v", err)
	}
	if err := db.QueryRowContext(ctx, `PRAGMA page_size`).Scan(&pageSize); err != nil {
		t.Fatalf("page_size: %v", err)
	}
	// Leave a small but non-zero budget so the failure happens after a handful
	// of writes instead of on the first one.
	limitedPages := pageCount + 8
	if _, err := db.ExecContext(ctx, fmt.Sprintf(`PRAGMA max_page_count = %d`, limitedPages)); err != nil {
		t.Fatalf("limit max_page_count: %v", err)
	}

	payload := make([]byte, 1024)
	for index := range payload {
		payload[index] = 'f'
	}
	var fullErr error
	inserted := 0
	for attempt := 0; attempt < 4096; attempt++ {
		if _, err := db.ExecContext(ctx, `INSERT INTO stability_disk_full (payload) VALUES (?)`, payload); err != nil {
			fullErr = err
			break
		}
		inserted++
	}

	run.Env["page_size"] = fmt.Sprint(pageSize)
	run.Env["limited_pages"] = fmt.Sprint(limitedPages)
	run.Env["inserted_before_full"] = fmt.Sprint(inserted)
	run.Check("fault.disk.full.errored", fullErr != nil, "inserted=%d without a write error", inserted)
	if fullErr != nil {
		run.Env["full_error"] = fullErr.Error()
		run.Check("fault.disk.full.error_indicates_full", isDiskFullError(fullErr), "err=%v", fullErr)
	}

	// The database must stay readable: a full disk may fail writes, never
	// corrupt what is already committed.
	rows := 0
	readErr := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM stability_disk_full`).Scan(&rows)
	run.Check("fault.disk.full.db_readable", readErr == nil && rows == inserted,
		"err=%v rows=%d want=%d", readErr, rows, inserted)

	// Space comes back: writing must recover without reopening or repairing.
	if _, err := db.ExecContext(ctx, `PRAGMA max_page_count = 1048576`); err != nil {
		t.Fatalf("restore max_page_count: %v", err)
	}
	_, recoverErr := db.ExecContext(ctx, `INSERT INTO stability_disk_full (payload) VALUES (?)`, payload)
	run.Check("fault.disk.full.recovers_after_space", recoverErr == nil, "err=%v", recoverErr)

	after, err := disk.Take(root)
	if err != nil {
		t.Fatalf("disk snapshot: %v", err)
	}
	run.AddDisk(after)
	run.Check("fault.disk.full.no_orphan_temp", after.TempFiles == 0, "temp_files=%d", after.TempFiles)
	run.Metric("fault.disk.full.rows_committed", float64(rows))
	run.Finish()

	path, err := run.Write(reportDirForState(t))
	if err != nil {
		t.Fatalf("write report: %v", err)
	}
	if !run.Passed {
		t.Fatalf("disk-full scenario failed: %+v (report %s)", run.Checks, path)
	}
}

func isDiskFullError(err error) bool {
	var sqliteErr *modernsqlite.Error
	if errors.As(err, &sqliteErr) && sqliteErr.Code() == sqliteFullCode {
		return true
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "full") || strings.Contains(message, "no space")
}
