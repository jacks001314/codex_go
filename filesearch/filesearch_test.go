package filesearch

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestScoreFuzzyMatchesAndReturnsIndices(t *testing.T) {
	score, indices, ok := Score("fr", "foo/report.txt")
	if !ok {
		t.Fatalf("expected match")
	}
	if score <= 0 {
		t.Fatalf("expected positive score, got %d", score)
	}
	if len(indices) != 2 || indices[0] != 0 || indices[1] != 4 {
		t.Fatalf("unexpected indices: %#v", indices)
	}
}

func TestRunReturnsFilesAndDirectories(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "docs", "guides"), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "docs", "guides", "intro.md"), []byte("intro"), 0o600); err != nil {
		t.Fatalf("write intro: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "docs", "readme.md"), []byte("readme"), 0o600); err != nil {
		t.Fatalf("write readme: %v", err)
	}
	results, err := Run(context.Background(), "guides", []string{dir}, Options{Limit: 20})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	foundDir := false
	for _, match := range results.Matches {
		if match.Path == "docs/guides" && match.MatchType == MatchDirectory {
			foundDir = true
		}
	}
	if !foundDir {
		t.Fatalf("expected directory match, got %#v", results.Matches)
	}
}

func TestRunHonorsExcludesAndLimit(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "alpha.txt"), []byte("alpha"), 0o600); err != nil {
		t.Fatalf("write alpha: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "alpha.log"), []byte("alpha"), 0o600); err != nil {
		t.Fatalf("write log: %v", err)
	}
	results, err := Run(context.Background(), "alpha", []string{dir}, Options{Limit: 1, Exclude: []string{"*.log"}})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if results.TotalMatchCount != 1 || len(results.Matches) != 1 || results.Matches[0].Path != "alpha.txt" {
		t.Fatalf("unexpected results: %#v", results)
	}
}

func TestFileNameFromPath(t *testing.T) {
	if got := FileNameFromPath("foo/bar.txt"); got != "bar.txt" {
		t.Fatalf("unexpected basename: %q", got)
	}
	if got := FileNameFromPath(""); got != "" {
		t.Fatalf("empty path should fall back to input: %q", got)
	}
}
