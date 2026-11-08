package state

import (
	"context"
	"database/sql"
	"strings"
	"testing"
)

// TestLogsPruneWindowUsesSinglePassLikeRust pins Rust #60006(c): the size and
// row windows state their frame explicitly, so both run as one ordered pass
// over the partition, and the retained rows are still exactly the newest ones
// the size and row limits allow.
func TestLogsPruneWindowUsesSinglePassLikeRust(t *testing.T) {
	ctx := context.Background()
	db := logsDBWithAges(t, nil)
	const rows = 1200
	for i := 0; i < rows; i++ {
		if _, err := db.ExecContext(ctx,
			`INSERT INTO logs (ts, ts_nanos, level, target, feedback_log_body, estimated_bytes) VALUES (?, ?, 'INFO', 'prune-window', 'x', 1)`,
			int64(i), int64(i)); err != nil {
			t.Fatalf("insert log %d: %v", i, err)
		}
	}
	statement := logPruneWindowStatement(`thread_id IS NULL AND process_uuid IS NULL`)
	if frames := strings.Count(statement, "ROWS UNBOUNDED PRECEDING"); frames != 2 {
		t.Fatalf("frame clauses = %d, want one per window function", frames)
	}

	plan := explainLogsPrunePlan(t, db, statement, logPartitionSizeLimitBytes, logPartitionRowLimit)
	coRoutines := 0
	for _, node := range plan {
		if strings.Contains(node, "CO-ROUTINE") {
			coRoutines++
		}
	}
	t.Logf("prune plan:\n%s", strings.Join(plan, "\n"))
	if coRoutines > 2 {
		t.Fatalf("prune plan uses %d co-routines, want the two windows folded into one pass:\n%s", coRoutines, strings.Join(plan, "\n"))
	}

	if err := pruneLogPartitionInTx(t, db, `thread_id IS NULL AND process_uuid IS NULL`); err != nil {
		t.Fatalf("pruneLogPartition() error = %v", err)
	}
	var kept int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM logs`).Scan(&kept); err != nil {
		t.Fatalf("count retained rows: %v", err)
	}
	if int64(kept) != logPartitionRowLimit {
		t.Fatalf("retained rows = %d, want the row limit %d", kept, logPartitionRowLimit)
	}
	var oldest, newest int64
	if err := db.QueryRowContext(ctx, `SELECT MIN(ts), MAX(ts) FROM logs`).Scan(&oldest, &newest); err != nil {
		t.Fatalf("read retained range: %v", err)
	}
	if oldest != int64(rows)-logPartitionRowLimit || newest != int64(rows)-1 {
		t.Fatalf("retained ts range = [%d, %d], want [%d, %d]", oldest, newest, int64(rows)-logPartitionRowLimit, int64(rows)-1)
	}
}

func explainLogsPrunePlan(t *testing.T, db *sql.DB, statement string, args ...any) []string {
	t.Helper()
	rows, err := db.Query(`EXPLAIN QUERY PLAN `+statement, args...)
	if err != nil {
		t.Fatalf("explain prune plan: %v", err)
	}
	defer rows.Close()
	var plan []string
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatalf("scan plan row: %v", err)
		}
		plan = append(plan, detail)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate plan rows: %v", err)
	}
	return plan
}

func pruneLogPartitionInTx(t *testing.T, db *sql.DB, predicate string) error {
	t.Helper()
	tx, err := db.Begin()
	if err != nil {
		t.Fatalf("begin prune transaction: %v", err)
	}
	if err := pruneLogPartition(context.Background(), tx, predicate, nil); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}
