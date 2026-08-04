package state

import (
	"context"
	"path/filepath"
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
