package memories

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"codex_go/state"
)

const fixedRolloutPrefix = "2025-02-11T15-35-19-jqmb"

func TestRolloutSummaryFileStemMatchesRust(t *testing.T) {
	base := state.Stage1Output{
		ThreadID:        "0194f5a6-89ab-7cde-8123-456789abcdef",
		SourceUpdatedAt: time.Unix(123, 0).UTC(),
	}
	if got := RolloutSummaryFileStem(base); got != fixedRolloutPrefix {
		t.Fatalf("stem without slug = %q", got)
	}
	base.RolloutSlug = "Unsafe Slug/With Spaces & Symbols + EXTRA_LONG_12345_67890_ABCDE_fghij_klmno"
	wantSlug := "unsafe_slug_with_spaces___symbols___extra_long_12345_67890_a"
	if got := RolloutSummaryFileStem(base); got != fixedRolloutPrefix+"-"+wantSlug {
		t.Fatalf("stem with slug = %q", got)
	}
	base.RolloutSlug = "   "
	if got := RolloutSummaryFileStem(base); got != fixedRolloutPrefix {
		t.Fatalf("stem with empty sanitized slug = %q", got)
	}

	fallback := state.Stage1Output{ThreadID: "not-a-uuid", SourceUpdatedAt: time.Date(2026, 7, 1, 2, 3, 4, 0, time.UTC)}
	if got := RolloutSummaryFileStem(fallback); !strings.HasPrefix(got, "2026-07-01T02-03-04-") || len(got) != len("2026-07-01T02-03-04-")+4 {
		t.Fatalf("fallback stem = %q", got)
	}
}

func TestSyncRolloutSummariesAndRawMemoriesMatchesRust(t *testing.T) {
	root := filepath.Join(t.TempDir(), "memories")
	if err := EnsureLayout(root); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(RolloutSummariesDir(root), "stale.md"), []byte("stale"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(RolloutSummariesDir(root), "keep.txt"), []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	values := []state.Stage1Output{
		{
			ThreadID: "0194f5a6-89ab-7cde-8123-456789abcdef", SourceUpdatedAt: time.Unix(100, 0).UTC(),
			RawMemory: "\n raw memory \n", RolloutSummary: "short summary", RolloutPath: "/tmp/rollout-100.jsonl",
			CWD: "/tmp/workspace", GitBranch: "main",
		},
		{
			ThreadID: "0194f5a6-89ab-7cde-8123-456789abcdee", SourceUpdatedAt: time.Unix(99, 0).UTC(),
			RawMemory: "dropped", RolloutSummary: "dropped", RolloutPath: "/tmp/dropped.jsonl", CWD: "/tmp/dropped",
		},
	}
	if err := SyncRolloutSummaries(root, values, 1); err != nil {
		t.Fatal(err)
	}
	if err := RebuildRawMemoriesFile(root, values, 1); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(RolloutSummariesDir(root))
	if err != nil {
		t.Fatal(err)
	}
	var markdown []string
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".md") {
			markdown = append(markdown, entry.Name())
		}
	}
	if len(markdown) != 1 || markdown[0] != RolloutSummaryFileStem(values[0])+".md" {
		t.Fatalf("summary markdown files = %v", markdown)
	}
	if _, err := os.Stat(filepath.Join(RolloutSummariesDir(root), "keep.txt")); err != nil {
		t.Fatalf("non-markdown file was pruned: %v", err)
	}

	summary, err := os.ReadFile(filepath.Join(RolloutSummariesDir(root), markdown[0]))
	if err != nil {
		t.Fatal(err)
	}
	wantSummary := "thread_id: 0194f5a6-89ab-7cde-8123-456789abcdef\n" +
		"updated_at: 1970-01-01T00:01:40+00:00\n" +
		"rollout_path: /tmp/rollout-100.jsonl\n" +
		"cwd: /tmp/workspace\n" +
		"git_branch: main\n\nshort summary\n"
	if string(summary) != wantSummary {
		t.Fatalf("rollout summary:\n%s\nwant:\n%s", summary, wantSummary)
	}

	raw, err := os.ReadFile(RawMemoriesFile(root))
	if err != nil {
		t.Fatal(err)
	}
	wantRaw := "# Raw Memories\n\n" +
		"Merged stage-1 raw memories (stable ascending thread-id order):\n\n" +
		"## Thread `0194f5a6-89ab-7cde-8123-456789abcdef`\n" +
		"updated_at: 1970-01-01T00:01:40+00:00\n" +
		"cwd: /tmp/workspace\n" +
		"rollout_path: /tmp/rollout-100.jsonl\n" +
		"rollout_summary_file: " + RolloutSummaryFileStem(values[0]) + ".md\n\n" +
		"raw memory\n\n"
	if string(raw) != wantRaw {
		t.Fatalf("raw memories:\n%s\nwant:\n%s", raw, wantRaw)
	}
}

func TestRebuildRawMemoriesEmpty(t *testing.T) {
	root := filepath.Join(t.TempDir(), "memories")
	if err := RebuildRawMemoriesFile(root, nil, 10); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(RawMemoriesFile(root))
	if err != nil || string(data) != "# Raw Memories\n\nNo raw memories yet.\n" {
		t.Fatalf("empty raw memories = %q, %v", data, err)
	}
}

func TestClearRootsContentsPreservesRootsAndRejectsSymlink(t *testing.T) {
	home := t.TempDir()
	for _, root := range []string{Root(home), filepath.Join(home, "memories_extensions")} {
		if err := os.MkdirAll(filepath.Join(root, "nested"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, "nested", "stale.md"), []byte("stale"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := ClearRootsContents(home); err != nil {
		t.Fatal(err)
	}
	for _, root := range []string{Root(home), filepath.Join(home, "memories_extensions")} {
		entries, err := os.ReadDir(root)
		if err != nil || len(entries) != 0 {
			t.Fatalf("root %s entries = %v, %v", root, entries, err)
		}
	}

	target := filepath.Join(home, "outside")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "keep.txt"), []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	linkHome := filepath.Join(home, "linked-home")
	if err := os.MkdirAll(linkHome, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, Root(linkHome)); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := ClearRootsContents(linkHome); err == nil || !strings.Contains(err.Error(), "refusing to clear symlinked memory root") {
		t.Fatalf("symlink clear error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(target, "keep.txt")); err != nil {
		t.Fatalf("symlink target was modified: %v", err)
	}
}
