package state

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"codex_go/rollout"
)

func TestPreparePaginatedForkUsesPhysicalProjectionBoundaries(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	runtime, err := InitStateRuntime(ctx, mustSQLiteConfig(t, home), "openai")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close() })
	path := writePaginatedHistoryFixture(t, home, "fork-source", nil, []historyFixtureTurn{
		{ID: "turn-1", Items: []historyFixtureItem{{ID: "user-1", Type: "userMessage", Text: "one"}}},
		{ID: "turn-2", Items: []historyFixtureItem{{ID: "user-2", Type: "userMessage", Text: "two"}}},
		{ID: "turn-active", InProgress: true},
	})
	if err := runtime.ReconcileRollout(ctx, path, false); err != nil {
		t.Fatal(err)
	}

	latest, err := runtime.PreparePaginatedFork(ctx, home, "fork-source", ThreadHistoryForkLatest, "")
	if err != nil {
		t.Fatal(err)
	}
	db, err := runtime.ThreadHistoryDB(ctx)
	if err != nil {
		t.Fatal(err)
	}
	projection, err := LoadThreadHistoryProjectionState(ctx, db, "fork-source")
	if err != nil {
		t.Fatal(err)
	}
	if latest == nil || latest.ThreadID != "fork-source" || latest.EndOrdinalExclusive != projection.NextRolloutOrdinal || latest.EndByteOffset != projection.NextRolloutByteOffset {
		t.Fatalf("latest position = %#v, projection = %#v", latest, projection)
	}

	through, err := runtime.PreparePaginatedFork(ctx, home, "fork-source", ThreadHistoryForkThroughTurn, "turn-1")
	if err != nil {
		t.Fatal(err)
	}
	var endOrdinal, endOffset int64
	if err := db.QueryRowContext(ctx, `SELECT rollout_end_ordinal, rollout_end_byte_offset FROM thread_turns WHERE thread_id = ? AND turn_id = ?`, "fork-source", "turn-1").Scan(&endOrdinal, &endOffset); err != nil {
		t.Fatal(err)
	}
	if through == nil || through.EndOrdinalExclusive != uint64(endOrdinal+1) || through.EndByteOffset != uint64(endOffset) {
		t.Fatalf("through position = %#v, row = %d/%d", through, endOrdinal, endOffset)
	}

	before, err := runtime.PreparePaginatedFork(ctx, home, "fork-source", ThreadHistoryForkBeforeTurn, "turn-2")
	if err != nil {
		t.Fatal(err)
	}
	var startOrdinal, startOffset int64
	if err := db.QueryRowContext(ctx, `SELECT rollout_ordinal, rollout_byte_offset FROM thread_turns WHERE thread_id = ? AND turn_id = ?`, "fork-source", "turn-2").Scan(&startOrdinal, &startOffset); err != nil {
		t.Fatal(err)
	}
	if before == nil || before.EndOrdinalExclusive != uint64(startOrdinal) || before.EndByteOffset != uint64(startOffset) {
		t.Fatalf("before position = %#v, row = %d/%d", before, startOrdinal, startOffset)
	}
	if first, err := runtime.PreparePaginatedFork(ctx, home, "fork-source", ThreadHistoryForkBeforeTurn, "turn-1"); err != nil || first != nil {
		t.Fatalf("before first turn = %#v, err %v", first, err)
	}
	if _, err := runtime.PreparePaginatedFork(ctx, home, "fork-source", ThreadHistoryForkThroughTurn, "turn-active"); !isInvalidHistoryError(err) {
		t.Fatalf("through active error = %v", err)
	}
	if _, err := runtime.PreparePaginatedFork(ctx, home, "fork-source", ThreadHistoryForkThroughTurn, "missing"); !isInvalidHistoryError(err) {
		t.Fatalf("missing turn error = %v", err)
	}

	childPath := writePaginatedHistoryFixture(t, home, "fork-empty-child", through, nil)
	if err := runtime.ReconcileRollout(ctx, childPath, false); err != nil {
		t.Fatal(err)
	}
	childLatest, err := runtime.PreparePaginatedFork(ctx, home, "fork-empty-child", ThreadHistoryForkLatest, "")
	if err != nil {
		t.Fatal(err)
	}
	if childLatest == nil || *childLatest != *through {
		t.Fatalf("empty child latest = %#v, want collapsed %#v", childLatest, through)
	}
}

func TestPreparePaginatedForkMaterializesCompressedLineageForReference(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	runtime, err := InitStateRuntime(ctx, mustSQLiteConfig(t, home), "openai")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close() })
	path := writePaginatedHistoryFixture(t, home, "fork-compressed", nil, []historyFixtureTurn{{
		ID: "turn-1", Items: []historyFixtureItem{{ID: "user-1", Type: "userMessage", Text: "compressed"}},
	}})
	if err := runtime.ReconcileRollout(ctx, path, false); err != nil {
		t.Fatal(err)
	}
	compressed := compressBackfillRollout(t, path)
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}

	position, err := runtime.PreparePaginatedFork(ctx, home, "fork-compressed", ThreadHistoryForkLatest, "")
	if err != nil {
		t.Fatal(err)
	}
	if position == nil || position.ThreadID != "fork-compressed" {
		t.Fatalf("compressed fork position = %#v", position)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("plain referenced rollout missing: %v", err)
	}
	if _, err := os.Stat(compressed); !os.IsNotExist(err) {
		t.Fatalf("compressed referenced rollout still exists: %v", err)
	}
}

func TestPreparePaginatedForkBeforeTerminalOnlyTurnRejectsMissingStart(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	runtime, err := InitStateRuntime(ctx, mustSQLiteConfig(t, home), "openai")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close() })
	now := time.Date(2026, 7, 31, 5, 6, 7, 0, time.UTC)
	path := writePaginatedHistoryFixture(t, home, "fork-terminal-only", nil, nil)
	if err := runtime.ReconcileRollout(ctx, path, false); err != nil {
		t.Fatal(err)
	}
	db, err := runtime.ThreadHistoryDB(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := MaterializeThreadHistory(ctx, db, "fork-terminal-only", path, 0, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO thread_turns (
thread_id, turn_id, rollout_ordinal, rollout_byte_offset, rollout_end_ordinal, rollout_end_byte_offset, status, started_at, completed_at, duration_ms
) VALUES (?, ?, 1, 100, 1, 200, 'completed', ?, ?, 1000)`, "fork-terminal-only", "terminal-only", now.Unix(), now.Add(time.Second).Unix()); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.PreparePaginatedFork(ctx, home, "fork-terminal-only", ThreadHistoryForkBeforeTurn, "terminal-only"); !isInvalidHistoryError(err) {
		t.Fatalf("terminal-only before error = %v", err)
	}
}

// Mirrors Rust #42135 `thread_fork_accepts_resumed_rollout_under_symlinked_sessions_root`:
// a resumed rollout that lives under a symlinked managed `sessions` root stays a
// valid reference because lineage validation compares canonical paths.
func TestPreparePaginatedForkAcceptsRolloutUnderSymlinkedSessionsRoot(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	externalSessions := filepath.Join(t.TempDir(), "sessions")
	if err := os.MkdirAll(externalSessions, 0o755); err != nil {
		t.Fatal(err)
	}
	linkTestDir(t, externalSessions, filepath.Join(home, rollout.SessionsSubdir))
	runtime, err := InitStateRuntime(ctx, mustSQLiteConfig(t, home), "openai")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close() })
	path := writePaginatedHistoryFixture(t, home, "fork-symlinked-sessions", nil, []historyFixtureTurn{{
		ID: "turn-1", Items: []historyFixtureItem{{ID: "user-1", Type: "userMessage", Text: "symlinked"}},
	}})
	canonicalPath, err := canonicalizePath(path)
	if err != nil {
		t.Fatal(err)
	}
	canonicalRoot, err := canonicalizePath(externalSessions)
	if err != nil {
		t.Fatal(err)
	}
	if !pathWithinRoot(canonicalPath, canonicalRoot) {
		t.Fatalf("fixture rollout %s did not land under the symlinked sessions root %s", canonicalPath, canonicalRoot)
	}
	if err := runtime.ReconcileRollout(ctx, path, false); err != nil {
		t.Fatal(err)
	}

	position, err := runtime.PreparePaginatedFork(ctx, home, "fork-symlinked-sessions", ThreadHistoryForkLatest, "")
	if err != nil {
		t.Fatalf("PreparePaginatedFork() error = %v", err)
	}
	if position == nil || position.ThreadID != "fork-symlinked-sessions" {
		t.Fatalf("fork position = %#v", position)
	}
}

// Mirrors Rust #42135 `rejects_reference_lineage_escaping_symlinked_sessions_root`:
// a nested symlink below the managed root must not let a reference lineage reach
// a rollout outside canonical `sessions`/`archived_sessions`.
func TestPreparePaginatedForkRejectsReferenceEscapingSymlinkedSessionsRoot(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	externalSessions := filepath.Join(t.TempDir(), "sessions")
	if err := os.MkdirAll(externalSessions, 0o755); err != nil {
		t.Fatal(err)
	}
	linkTestDir(t, externalSessions, filepath.Join(home, rollout.SessionsSubdir))
	escapedRollouts := filepath.Join(t.TempDir(), "rollouts")
	if err := os.MkdirAll(escapedRollouts, 0o755); err != nil {
		t.Fatal(err)
	}
	linkTestDir(t, escapedRollouts, filepath.Join(externalSessions, "2026"))
	runtime, err := InitStateRuntime(ctx, mustSQLiteConfig(t, home), "openai")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close() })
	path := writePaginatedHistoryFixture(t, home, "fork-escaped-sessions", nil, []historyFixtureTurn{{
		ID: "turn-1", Items: []historyFixtureItem{{ID: "user-1", Type: "userMessage", Text: "escaped"}},
	}})
	canonicalPath, err := canonicalizePath(path)
	if err != nil {
		t.Fatal(err)
	}
	canonicalEscaped, err := canonicalizePath(escapedRollouts)
	if err != nil {
		t.Fatal(err)
	}
	if !pathWithinRoot(canonicalPath, canonicalEscaped) {
		t.Fatalf("fixture rollout %s did not escape through the nested symlink %s", canonicalPath, canonicalEscaped)
	}
	if err := runtime.ReconcileRollout(ctx, path, false); err != nil {
		t.Fatal(err)
	}

	_, err = runtime.PreparePaginatedFork(ctx, home, "fork-escaped-sessions", ThreadHistoryForkLatest, "")
	if !isInvalidHistoryError(err) || !strings.Contains(err.Error(), "must be in Codex home directory") {
		t.Fatalf("escaping fork error = %v, want a Codex home scope rejection", err)
	}
}

// linkTestDir creates a directory link. Windows symlinks need a privilege that
// unprivileged test runs do not hold, so fall back to a junction, which Rust's
// canonicalize follows in the same way.
func linkTestDir(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err == nil {
		return
	}
	if runtime.GOOS == "windows" {
		output, err := exec.Command("cmd", "/c", "mklink", "/J", link, target).CombinedOutput()
		if err == nil {
			return
		}
		t.Skipf("directory links are unavailable: %v: %s", err, strings.TrimSpace(string(output)))
	}
	t.Skip("directory symlinks are unavailable")
}
