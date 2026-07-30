package diffview

import (
	"strings"
	"testing"

	codextui "codex_go/tui"
)

func TestNewViewDefaults(t *testing.T) {
	v := NewView(100)
	if v.Width != 100 {
		t.Errorf("width = %d, want 100", v.Width)
	}
	if v.Layout != LayoutUnified {
		t.Error("default layout should be unified")
	}
	if !v.ShowLineNum {
		t.Error("default should show line numbers")
	}
}

func TestViewRenderEmpty(t *testing.T) {
	v := NewView(80)
	out := v.Render()
	if out != "" {
		t.Errorf("empty view should render empty, got %q", out)
	}
}

func TestViewRenderUnified(t *testing.T) {
	v := NewView(80)
	v.AddFile(FileDiff{
		Path: "test.go",
		Hunks: []Hunk{
			{
				OldStart: 1, NewStart: 1,
				Lines: []DiffLine{
					{Kind: LineContext, Content: "package main", OldNum: 1, NewNum: 1},
					{Kind: LineDelete, Content: "old line", OldNum: 2},
					{Kind: LineInsert, Content: "new line", NewNum: 2},
					{Kind: LineContext, Content: "}", OldNum: 3, NewNum: 3},
				},
			},
		},
	})

	out := v.Render()
	if out == "" {
		t.Fatal("rendered output is empty")
	}
	if !strings.Contains(out, "test.go") {
		t.Error("missing file header")
	}
	// Check for unified diff markers
	if !strings.Contains(out, "+new line") {
		t.Error("missing inserted line")
	}
	if !strings.Contains(out, "-old line") {
		t.Error("missing deleted line")
	}
}

func TestViewRenderSplit(t *testing.T) {
	v := NewView(80)
	v.Layout = LayoutSplit
	v.AddFile(FileDiff{
		Path: "test.go",
		Hunks: []Hunk{
			{
				OldStart: 1, NewStart: 1,
				Lines: []DiffLine{
					{Kind: LineDelete, Content: "deleted", OldNum: 1},
					{Kind: LineInsert, Content: "inserted", NewNum: 1},
				},
			},
		},
	})

	out := v.Render()
	if out == "" {
		t.Fatal("rendered output is empty")
	}
	// Split view should have separator
	if !strings.Contains(out, "│") {
		t.Error("split view missing separator")
	}
}

func TestViewWithoutLineNumbers(t *testing.T) {
	v := NewView(80)
	v.ShowLineNum = false
	v.AddFile(FileDiff{
		Path: "test.go",
		Hunks: []Hunk{
			{
				Lines: []DiffLine{
					{Kind: LineInsert, Content: "added", NewNum: 1},
				},
			},
		},
	})
	out := v.Render()
	if !strings.Contains(out, "+added") {
		t.Error("missing content without line numbers")
	}
}

func TestParseUnifiedDiff(t *testing.T) {
	diff := `diff --git a/main.go b/main.go
--- a/main.go
+++ b/main.go
@@ -1,3 +1,3 @@
 package main
-oldFunc()
+newFunc()
`

	files := ParseUnifiedDiff(diff)
	if len(files) != 1 {
		t.Fatalf("expected 1 file, got %d", len(files))
	}
	if files[0].Path != "main.go" {
		t.Errorf("path = %q, want main.go", files[0].Path)
	}
	if files[0].OldPath != "main.go" {
		t.Errorf("oldPath = %q, want main.go", files[0].OldPath)
	}
	if len(files[0].Hunks) != 1 {
		t.Fatalf("expected 1 hunk, got %d", len(files[0].Hunks))
	}
	if len(files[0].Hunks[0].Lines) != 3 {
		t.Fatalf("expected 3 lines, got %d", len(files[0].Hunks[0].Lines))
	}
	// Check line kinds
	hunk := files[0].Hunks[0]
	if hunk.Lines[0].Kind != LineContext || hunk.Lines[0].Content != "package main" {
		t.Error("first line should be context")
	}
	if hunk.Lines[1].Kind != LineDelete || hunk.Lines[1].Content != "oldFunc()" {
		t.Error("second line should be delete")
	}
	if hunk.Lines[2].Kind != LineInsert || hunk.Lines[2].Content != "newFunc()" {
		t.Error("third line should be insert")
	}
}

func TestParseUnifiedDiffEmpty(t *testing.T) {
	files := ParseUnifiedDiff("")
	if len(files) != 0 {
		t.Errorf("expected 0 files, got %d", len(files))
	}
}

func TestParseUnifiedDiffMultiFile(t *testing.T) {
	diff := `diff --git a/a.go b/a.go
--- a/a.go
+++ b/a.go
@@ -1,1 +1,1 @@
-old
+new
diff --git a/b.go b/b.go
--- a/b.go
+++ b/b.go
@@ -1,1 +1,1 @@
-old2
+new2
`
	files := ParseUnifiedDiff(diff)
	if len(files) != 2 {
		t.Fatalf("expected 2 files, got %d", len(files))
	}
}

func TestTruncateLine(t *testing.T) {
	v := NewView(10)
	line := "this is a very long line"
	result := v.truncateLine(line)
	runes := []rune(result)
	if len(runes) > 10 {
		t.Errorf("truncated line has %d runes, want <= 10: %q", len(runes), result)
	}
}

func TestTruncateLineUsesTerminalGraphemeWidth(t *testing.T) {
	v := NewView(5)
	if got := v.truncateLine("ab\uff76\uff9ecd"); got != "ab\uff76\uff9e\u2026" {
		t.Fatalf("halfwidth truncateLine = %q", got)
	}
	if got := v.truncateLine("a\U0001f44d\U0001f3fbbcd"); codextui.DisplayWidth(got) > 5 || strings.Contains(got, "\U0001f44d\u2026") {
		t.Fatalf("emoji truncateLine = %q width=%d", got, codextui.DisplayWidth(got))
	}
}

func TestZeroWidth(t *testing.T) {
	v := NewView(0)
	v.AddFile(FileDiff{
		Path:  "x.go",
		Hunks: []Hunk{{Lines: []DiffLine{{Kind: LineContext, Content: "x"}}}},
	})
	out := v.Render()
	if out == "" {
		t.Error("zero width should default to 80")
	}
}
