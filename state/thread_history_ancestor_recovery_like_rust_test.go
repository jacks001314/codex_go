package state

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"

	"codex_go/rollout"
)

// TestThreadHistoryPageSkipsAncestorCutoffValidationLikeRust covers Rust
// #58960: history pages read projected rows and only need the ancestor's
// metadata, so a shorter ancestor file must not invalidate the page. Context
// loading still rejects the same incomplete cutoff.
func TestThreadHistoryPageSkipsAncestorCutoffValidationLikeRust(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	runtime, err := InitStateRuntime(ctx, mustSQLiteConfig(t, home), "openai")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close() })

	parentPath := writePaginatedHistoryFixture(t, home, "lineage-parent", nil, []historyFixtureTurn{{
		ID: "parent-turn", Items: []historyFixtureItem{{ID: "parent-item", Type: "userMessage", Text: "parent"}},
	}})
	if err := runtime.ReconcileRollout(ctx, parentPath, false); err != nil {
		t.Fatal(err)
	}
	parentBytes, err := os.ReadFile(parentPath)
	if err != nil {
		t.Fatal(err)
	}
	base := &rollout.HistoryPosition{
		ThreadID:            "lineage-parent",
		EndOrdinalExclusive: 1,
		EndByteOffset:       uint64(len(parentBytes)),
	}
	childPath := writePaginatedHistoryFixture(t, home, "lineage-child", base, []historyFixtureTurn{{
		ID: "child-turn", Items: []historyFixtureItem{{ID: "child-item", Type: "userMessage", Text: "child"}},
	}})
	if err := runtime.ReconcileRollout(ctx, childPath, false); err != nil {
		t.Fatal(err)
	}

	// The ancestor keeps its metadata but loses the tail the cutoff names.
	metaEnd := bytes.IndexByte(parentBytes, '\n') + 1
	if metaEnd <= 1 {
		t.Fatal("parent fixture has no metadata line")
	}
	if err := os.WriteFile(parentPath, parentBytes[:metaEnd], 0o644); err != nil {
		t.Fatal(err)
	}

	lineage, err := runtime.resolveThreadHistoryLineage(ctx, "lineage-child", true)
	if err != nil {
		t.Fatalf("history page lineage error = %v", err)
	}
	if len(lineage) != 2 || lineage[0].ThreadID != "lineage-parent" || lineage[1].ThreadID != "lineage-child" {
		t.Fatalf("history page lineage = %#v", lineage)
	}
	if _, err := runtime.resolveThreadHistoryLineage(ctx, "lineage-child", false); err == nil ||
		!strings.Contains(err.Error(), "cutoff byte offset is past the source rollout") {
		t.Fatalf("context lineage error = %v, want the cutoff rejection", err)
	}
}
