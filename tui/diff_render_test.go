package tui

import (
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"codex_go/utils"
)

func TestFileChangeLineCounts(t *testing.T) {
	if added, removed := FileChangeLineCounts(NewAddFileChange("a\nb\n")); added != 2 || removed != 0 {
		t.Fatalf("add counts = +%d -%d", added, removed)
	}
	if added, removed := FileChangeLineCounts(NewDeleteFileChange("a\nb\nc\n")); added != 0 || removed != 3 {
		t.Fatalf("delete counts = +%d -%d", added, removed)
	}
	diff := "--- a/file.go\n+++ b/file.go\n@@ -1,2 +1,3 @@\n old\n-removed\n+added\n+added2\n"
	if added, removed := CalculateAddRemoveFromDiff(diff); added != 2 || removed != 1 {
		t.Fatalf("diff counts = +%d -%d", added, removed)
	}
}

func TestCollectDiffRowsSortsAndSummarizes(t *testing.T) {
	rows := CollectDiffRows(map[string]FileChange{
		"b.txt": NewAddFileChange("one\n"),
		"a.txt": NewDeleteFileChange("old\nold2\n"),
	})
	if len(rows) != 2 || rows[0].Path != "a.txt" || rows[1].Path != "b.txt" {
		t.Fatalf("rows = %#v", rows)
	}
	if rows[0].Removed != 2 || rows[1].Added != 1 {
		t.Fatalf("row counts = %#v", rows)
	}
	if got := utils.StripANSI(RenderLineCountSummary(12, 3)); got != "(+12 -3)" {
		t.Fatalf("summary = %q", got)
	}
}

func TestCreateDiffPreviewLimitsRowsLikeRust(t *testing.T) {
	content := ""
	for i := 0; i < 40; i++ {
		content += "line" + strconv.Itoa(i) + "\n"
	}
	changes := map[string]FileChange{
		"a.txt": {Type: FileChangeAdd, Content: content},
		"b.txt": {Type: FileChangeAdd, Content: content},
		"c.txt": {Type: FileChangeAdd, Content: content},
	}
	preview := CreateDiffPreview(changes, "/repo", 80, "dark")
	if !strings.Contains(strings.Join(preview, "\n"), "additional lines omitted") {
		t.Fatalf("preview missing omission notice: %#v", preview)
	}
	full := CreateDiffSummary(changes, "/repo", 80, "dark")
	if strings.Contains(strings.Join(full, "\n"), "additional lines omitted") {
		t.Fatalf("full summary should not contain omission notice")
	}
	if len(full) <= len(preview) {
		t.Fatalf("preview (%d) should be shorter than full (%d)", len(preview), len(full))
	}
}

func TestRenderFileChangeAddDeleteAndUpdate(t *testing.T) {
	add := RenderFileChange(NewAddFileChange("first\nsecond\n"), 40, "dark", "")
	if len(add) != 2 || !strings.Contains(utils.StripANSI(add[0]), "1 + first") || !strings.Contains(utils.StripANSI(add[1]), "2 + second") {
		t.Fatalf("add render = %#v", add)
	}
	del := RenderFileChange(NewDeleteFileChange("gone\n"), 40, "dark", "")
	if len(del) != 1 || !strings.Contains(utils.StripANSI(del[0]), "1 - gone") {
		t.Fatalf("delete render = %#v", del)
	}
	diff := "@@ -10,2 +20,3 @@\n context\n-old\n+new\n+new2\n"
	update := RenderFileChange(NewUpdateFileChange(diff, ""), 40, "dark", "")
	joined := utils.StripANSI(strings.Join(update, "\n"))
	for _, want := range []string{"@@ -10,2 +20,3 @@", "20   context", "11 - old", "21 + new", "22 + new2"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("update render missing %q:\n%s", want, joined)
		}
	}
}

func TestRenderChangesBlockSingleMultiAndRename(t *testing.T) {
	cwd := t.TempDir()
	addPath := filepath.Join(cwd, "src", "a.go")
	renamePath := filepath.Join(cwd, "src", "b.go")

	single := CreateDiffSummary(map[string]FileChange{
		addPath: NewAddFileChange("package main\n"),
	}, cwd, 80, "dark")
	if len(single) == 0 || !strings.Contains(utils.StripANSI(single[0]), "Added src/a.go (+1 -0)") {
		t.Fatalf("single summary = %#v", single)
	}

	multi := CreateDiffSummary(map[string]FileChange{
		addPath:  NewAddFileChange("new\n"),
		"old.go": NewUpdateFileChange("@@ -1 +1 @@\n-old\n+new\n", renamePath),
	}, cwd, 80, "dark")
	joined := utils.StripANSI(strings.Join(multi, "\n"))
	for _, want := range []string{"Edited 2 files (+2 -1)", "old.go -> src/b.go", "src/a.go (+1 -0)"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("multi summary missing %q:\n%s", want, joined)
		}
	}
}

func TestDiffLineNumberWidthAndDisplayPath(t *testing.T) {
	if got := LineNumberWidth(999); got != 3 {
		t.Fatalf("LineNumberWidth = %d, want 3", got)
	}
	cwd := t.TempDir()
	path := filepath.Join(cwd, "nested", "file.txt")
	if got := DisplayDiffPath(path, cwd); got != "nested/file.txt" {
		t.Fatalf("DisplayDiffPath = %q", got)
	}
}
