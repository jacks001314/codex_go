package tui

import (
	"path/filepath"
	"testing"
)

// TestFormatInstructionSourcesLikeRust mirrors #40502: home paths collapse,
// while files inside or one level above the project remain relative.
func TestFormatInstructionSourcesLikeRust(t *testing.T) {
	home := t.TempDir()
	cwd := filepath.Join(home, "workspace", "project")
	paths := []string{
		filepath.Join(home, ".codex", "AGENTS.md"),
		filepath.Join(home, "workspace", "AGENTS.md"),
		filepath.Join(cwd, "AGENTS.md"),
		filepath.Join(cwd, "nested", "AGENTS.md"),
	}
	want := "~/.codex/AGENTS.md, ../AGENTS.md, AGENTS.md, nested/AGENTS.md"
	if got := FormatInstructionSources(paths, cwd, home); got != want {
		t.Fatalf("FormatInstructionSources = %q, want %q", got, want)
	}
}

func TestFormatInstructionSourcesDeduplicatesAndKeepsOutsidePaths(t *testing.T) {
	path := filepath.Join(t.TempDir(), "AGENTS.md")
	if got := FormatInstructionSources([]string{path, " " + path + " "}, "", ""); got != filepath.ToSlash(path) {
		t.Fatalf("deduplicated sources = %q, want %q", got, filepath.ToSlash(path))
	}
}
