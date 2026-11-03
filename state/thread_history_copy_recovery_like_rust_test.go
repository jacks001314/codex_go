package state

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codex_go/rollout"
)

// Rust #58959/#58960: a lineage segment still resolves when the indexed copy of
// its rollout is unreadable and another managed copy of the same thread exists.
func TestThreadHistoryLineageRetriesAlternateRolloutCopiesLikeRust(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	runtime, err := InitStateRuntime(ctx, mustSQLiteConfig(t, home), "openai")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close() })

	indexed := writePaginatedHistoryFixture(t, home, "lineage-copy", nil, []historyFixtureTurn{{
		ID:    "turn-1",
		Items: []historyFixtureItem{{ID: "item-1", Type: "userMessage", Text: "recovered"}},
	}})
	if err := runtime.ReconcileRollout(ctx, indexed, false); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(indexed)
	if err != nil {
		t.Fatal(err)
	}
	archived := filepath.Join(home, rollout.ArchivedSessionsSubdir, "rollout-2026-07-31T01-02-03-lineage-copy.jsonl")
	if err := os.MkdirAll(filepath.Dir(archived), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(archived, content, 0o644); err != nil {
		t.Fatal(err)
	}
	// The indexed copy loses its metadata line; only the archived copy remains
	// readable.
	if err := os.WriteFile(indexed, []byte("not a rollout\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	lineage, err := runtime.resolveThreadHistoryLineage(ctx, "lineage-copy", true)
	if err != nil {
		t.Fatalf("resolveThreadHistoryLineage() error = %v", err)
	}
	if len(lineage) != 1 {
		t.Fatalf("lineage = %#v", lineage)
	}
	if filepath.Clean(lineage[0].Path) != filepath.Clean(archived) {
		t.Fatalf("lineage path = %q, want the archived copy %q", lineage[0].Path, archived)
	}
	if lineage[0].Meta == nil || lineage[0].Meta.ID != "lineage-copy" {
		t.Fatalf("lineage metadata = %#v", lineage[0].Meta)
	}
}

// When every copy is unusable the preferred copy's error still wins, so a
// broken indexed rollout is reported as such instead of as an unrelated later
// failure.
func TestThreadHistoryLineageKeepsPreferredCopyErrorLikeRust(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	runtime, err := InitStateRuntime(ctx, mustSQLiteConfig(t, home), "openai")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close() })

	indexed := writePaginatedHistoryFixture(t, home, "lineage-broken", nil, []historyFixtureTurn{{
		ID:    "turn-1",
		Items: []historyFixtureItem{{ID: "item-1", Type: "userMessage", Text: "lost"}},
	}})
	if err := runtime.ReconcileRollout(ctx, indexed, false); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(indexed, []byte("truncated"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err = runtime.resolveThreadHistoryLineage(ctx, "lineage-broken", true)
	if err == nil {
		t.Fatal("resolveThreadHistoryLineage() succeeded for an unreadable rollout")
	}
	if want := fmt.Sprintf("read lineage metadata %s", indexed); !strings.Contains(err.Error(), want) {
		t.Fatalf("error = %v, want the preferred copy's metadata error", err)
	}
}
