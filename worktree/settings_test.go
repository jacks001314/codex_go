package worktree

import "testing"

func TestFromDesktopConfigDefaultsMatchRust(t *testing.T) {
	settings, err := FromDesktopConfig("C:\\home", nil)
	if err != nil {
		t.Fatalf("FromDesktopConfig() error = %v", err)
	}
	if settings.Root != "C:\\home\\worktrees" || !settings.AutoCleanupEnabled || settings.KeepCount != 15 {
		t.Fatalf("defaults = %#v, want root=C:\\home\\worktrees cleanup=true keep=15", settings)
	}
}

func TestFromDesktopConfigHonorsConfiguredValues(t *testing.T) {
	settings, err := FromDesktopConfig("C:\\home", map[string]any{
		"git-worktree-root":             `D:\\repo\\worktrees`,
		"worktree-auto-cleanup-enabled": false,
		"worktree-keep-count":           float64(7),
	})
	if err != nil {
		t.Fatalf("FromDesktopConfig() error = %v", err)
	}
	if settings.Root != `D:\repo\worktrees` || settings.AutoCleanupEnabled || settings.KeepCount != 7 {
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
