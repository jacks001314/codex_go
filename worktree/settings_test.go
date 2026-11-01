package worktree

import (
	"path/filepath"
	"testing"
)

// The Rust fixtures derive both the home and the configured root from a real
// temporary directory, so the defaults follow the host's path separator.
func TestFromDesktopConfigDefaultsMatchRust(t *testing.T) {
	home := t.TempDir()
	settings, err := FromDesktopConfig(home, nil)
	if err != nil {
		t.Fatalf("FromDesktopConfig() error = %v", err)
	}
	wantRoot := filepath.Join(home, "worktrees")
	if settings.Root != wantRoot || !settings.AutoCleanupEnabled || settings.KeepCount != 15 {
		t.Fatalf("defaults = %#v, want root=%s cleanup=true keep=15", settings, wantRoot)
	}
}

func TestFromDesktopConfigHonorsConfiguredValues(t *testing.T) {
	home := t.TempDir()
	customRoot := filepath.Join(home, "custom-managed-worktrees")
	settings, err := FromDesktopConfig(home, map[string]any{
		"git-worktree-root":             customRoot,
		"worktree-auto-cleanup-enabled": false,
		"worktree-keep-count":           float64(7),
	})
	if err != nil {
		t.Fatalf("FromDesktopConfig() error = %v", err)
	}
	if settings.Root != customRoot || settings.AutoCleanupEnabled || settings.KeepCount != 7 {
		t.Fatalf("configured = %#v", settings)
	}
}

func TestFromDesktopConfigRejectsInvalid(t *testing.T) {
	if _, err := FromDesktopConfig("C:\\home", map[string]any{"git-worktree-root": "relative"}); err == nil {
		t.Fatal("relative root accepted")
	}
	if _, err := FromDesktopConfig("C:\\home", map[string]any{"worktree-keep-count": 0}); err == nil {
		t.Fatal("zero keep-count accepted")
	}
	if _, err := FromDesktopConfig("C:\\home", map[string]any{"worktree-auto-cleanup-enabled": "yes"}); err == nil {
		t.Fatal("non-boolean cleanup accepted")
	}
}
