package state

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func TestListThreadRowsByNameFiltersCollectionAndOrdersByRecency(t *testing.T) {
	runtime := newBackfillTestRuntime(t)
	ctx := context.Background()
	insert := func(id string, name string, title string, archived bool, recency int64) {
		t.Helper()
		_, err := runtime.StateDB().ExecContext(ctx, `INSERT INTO threads
			(id, rollout_path, created_at, updated_at, created_at_ms, updated_at_ms, recency_at_ms, source, model_provider, cwd, title, name, sandbox_policy, approval_mode, archived)
			VALUES (?, ?, 1, 1, 1, 1, ?, 'cli', 'openai', '/workspace', ?, ?, '', '', ?)`,
			id, filepath.Join("sessions", id+".jsonl"), recency, title, name, boolInt(archived))
		if err != nil {
			t.Fatalf("insert %s: %v", id, err)
		}
	}
	insert("older-thread", "Work", "Older work", false, 100)
	insert("newer-thread", "Work", "Newer work", false, 200)
	insert("archived-thread", "Work", "Archived work", true, 300)

	active, err := runtime.ListThreadRowsByName(ctx, "Work", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(active) != 2 || active[0].ID != "newer-thread" || active[1].ID != "older-thread" {
		t.Fatalf("active rows = %#v", active)
	}
	archived, err := runtime.ListThreadRowsByName(ctx, "Work", true)
	if err != nil {
		t.Fatal(err)
	}
	if len(archived) != 1 || archived[0].ID != "archived-thread" {
		t.Fatalf("archived rows = %#v", archived)
	}
	missing, err := runtime.ListThreadRowsByName(ctx, "Missing", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(missing) != 0 {
		t.Fatalf("missing rows = %#v", missing)
	}
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

// Rust #61727: targeted partial indexes keep the default sidebar listing in
// timestamp order without scanning subagent threads, and let attached-thread
// listings seek by parent plus archive status.
func TestThreadListingIndexesMatchRustTarget(t *testing.T) {
	runtime := newBackfillTestRuntime(t)
	ctx := context.Background()
	indexes := map[string]string{
		"idx_threads_attached_parent":           "(attached_thread_parent_id, archived)",
		"idx_threads_interactive_created_at_ms": "created_at_ms DESC, id DESC",
		"idx_threads_interactive_updated_at_ms": "updated_at_ms DESC, id DESC",
		"idx_threads_interactive_recency_at_ms": "recency_at_ms DESC, id DESC",
	}
	for name, want := range indexes {
		var sqlText string
		if err := runtime.StateDB().QueryRowContext(ctx, `SELECT sql FROM sqlite_master WHERE type = 'index' AND name = ?`, name).Scan(&sqlText); err != nil {
			t.Fatalf("index %s: %v", name, err)
		}
		if !strings.Contains(sqlText, want) {
			t.Fatalf("index %s = %q, want %q", name, sqlText, want)
		}
	}

	rows, err := runtime.StateDB().QueryContext(ctx, `
EXPLAIN QUERY PLAN
SELECT threads.id FROM threads
WHERE archived = 0 AND preview <> '' AND attached_thread_parent_id IS NULL
    AND source IN ('cli', 'vscode', '{"custom":"atlas"}', '{"custom":"chatgpt"}')
ORDER BY updated_at_ms DESC, id DESC LIMIT 56`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	plan := ""
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		plan += detail + "\n"
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(plan, "idx_threads_interactive_updated_at_ms") {
		t.Fatalf("default sidebar plan = %q", plan)
	}
}
