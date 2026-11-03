package appserver

import (
	"context"
	"testing"
	"time"

	"codex_go/state"
)

// On the full schema, a thread metadata write advances recency in the same
// statement, so renaming refreshes recency-ordered listings (Rust #59016).
func TestUpdateRustStateThreadNameAdvancesRecencyAtomically(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	cfg, err := state.SqliteConfigForCodexHome(home)
	if err != nil {
		t.Fatalf("SqliteConfigForCodexHome() error = %v", err)
	}
	runtime, err := state.InitStateRuntime(ctx, cfg, "test-provider")
	if err != nil {
		t.Fatalf("InitStateRuntime() error = %v", err)
	}
	defer runtime.Close()
	now := time.Now().UTC().Add(-time.Hour)
	if _, err := runtime.StateDB().ExecContext(ctx, `
INSERT INTO threads (id, rollout_path, created_at, updated_at, recency_at, created_at_ms, updated_at_ms, recency_at_ms, source, model_provider, cwd, title, sandbox_policy, approval_mode, tokens_used)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, 'cli', 'test-provider', ?, '', 'workspace-write', 'never', 0)`,
		"rename-thread", home+"/rollout.jsonl", now.Unix(), now.Unix(), now.Unix(), now.UnixMilli(), now.UnixMilli(), now.UnixMilli(), home); err != nil {
		t.Fatalf("insert thread: %v", err)
	}
	if err := updateRustStateThreadName(home, "rename-thread", "New name"); err != nil {
		t.Fatalf("updateRustStateThreadName() error = %v", err)
	}
	var name string
	var recencyAt, recencyAtMS int64
	if err := runtime.StateDB().QueryRowContext(ctx,
		`SELECT name, recency_at, recency_at_ms FROM threads WHERE id = 'rename-thread'`).Scan(&name, &recencyAt, &recencyAtMS); err != nil {
		t.Fatalf("read renamed thread: %v", err)
	}
	if name != "New name" {
		t.Fatalf("name = %q", name)
	}
	if recencyAtMS <= now.UnixMilli() || recencyAt < now.Unix() {
		t.Fatalf("recency did not advance: at=%d ms=%d old=%d", recencyAt, recencyAtMS, now.UnixMilli())
	}
}
