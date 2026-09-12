package worktree

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestWorktreeManagerCreateAndList(t *testing.T) {
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not available")
	}
	source := t.TempDir()
	runGit(t, source, "init")
	runGit(t, source, "config", "user.email", "test@example.com")
	runGit(t, source, "config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(source, "file.txt"), []byte("hello\n"), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}
	runGit(t, source, "add", "file.txt")
	runGit(t, source, "commit", "-m", "initial")

	settings, err := FromDesktopConfig(t.TempDir(), nil)
	if err != nil {
		t.Fatalf("FromDesktopConfig() error = %v", err)
	}
	manager := NewWorktreeManager(settings)
	created, err := manager.Create(source, "")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if created.Root == "" || created.CWD != created.Root || created.HeadSHA == "" {
		t.Fatalf("Create() = %#v", created)
	}
	if _, err := os.Stat(filepath.Join(created.Root, "file.txt")); err != nil {
		t.Fatalf("created worktree missing file: %v", err)
	}
	list, err := manager.List(source)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(list) != 1 || list[0].Root != created.Root {
		t.Fatalf("List() = %#v, want %#v", list, created)
	}
	if err := manager.BindThread(created.Root, "thread-1"); err != nil {
		t.Fatalf("BindThread() error = %v", err)
	}
	owner, err := manager.Owner(created.Root)
	if err != nil {
		t.Fatalf("Owner() error = %v", err)
	}
	if owner != "thread-1" {
		t.Fatalf("Owner() = %q, want thread-1", owner)
	}
	if err := manager.BindThread(created.Root, "thread-1"); err != nil {
		t.Fatalf("rebind same owner error = %v", err)
	}
	if err := manager.BindThread(created.Root, "thread-2"); err == nil {
		t.Fatal("BindThread replaced existing owner")
	}
	if err := manager.Remove(created.Root); err != nil {
		t.Fatalf("Remove() error = %v", err)
	}
	if remaining, err := manager.List(source); err != nil || len(remaining) != 0 {
		t.Fatalf("List after Remove = %#v, %v", remaining, err)
	}
	_ = git
}

func TestWorktreeOwnerRejectsInvalidRecord(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	root := t.TempDir()
	runGit(t, root, "init")
	metadataPath, err := worktreeMetadataPath(root)
	if err != nil {
		t.Fatalf("worktreeMetadataPath() error = %v", err)
	}
	if err := os.WriteFile(metadataPath, []byte(`{"version":99,"ownerThreadId":"thread"}`), 0o600); err != nil {
		t.Fatalf("WriteFile metadata: %v", err)
	}
	manager := NewWorktreeManager(WorktreeSettings{Root: root, AutoCleanupEnabled: true, KeepCount: 15})
	if _, err := manager.Owner(root); err == nil {
		t.Fatal("Owner() should reject an invalid version record")
	}
}

func TestWorktreeOwnerReturnsEmptyWhenMissing(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	root := t.TempDir()
	runGit(t, root, "init")
	manager := NewWorktreeManager(WorktreeSettings{Root: root, AutoCleanupEnabled: true, KeepCount: 15})
	owner, err := manager.Owner(root)
	if err != nil {
		t.Fatalf("Owner() error = %v", err)
	}
	if owner != "" {
		t.Fatalf("Owner() = %q, want empty", owner)
	}
}

func TestWorktreeListExcludesNonManagedLayout(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	root := t.TempDir()
	runGit(t, root, "init")
	manager := NewWorktreeManager(WorktreeSettings{Root: filepath.Join(root, "managed"), AutoCleanupEnabled: true, KeepCount: 15})
	list, err := manager.List(root)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("List() = %#v, want no non-managed worktrees", list)
	}
}

// TestWorktreeManagerRemoveManagedSafety covers Rust #43942: managed removal
// refuses the current checkout and ignored local files, and removes a clean
// registered checkout.
func TestWorktreeManagerRemoveManagedSafety(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	source := t.TempDir()
	runGit(t, source, "init")
	runGit(t, source, "config", "user.email", "test@example.com")
	runGit(t, source, "config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(source, "file.txt"), []byte("hello\n"), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}
	runGit(t, source, "add", "file.txt")
	runGit(t, source, "commit", "-m", "initial")

	settings, err := FromDesktopConfig(t.TempDir(), nil)
	if err != nil {
		t.Fatalf("FromDesktopConfig() error = %v", err)
	}
	manager := NewWorktreeManager(settings)
	created, err := manager.Create(source, "")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	// An unrelated path is not a managed worktree of this repository.
	if err := manager.RemoveManaged(source, t.TempDir()); err == nil || !strings.Contains(err.Error(), "not a managed worktree") {
		t.Fatalf("unrelated root error = %v", err)
	}

	// The current checkout cannot delete itself, including through a path alias.
	if err := manager.RemoveManaged(created.Root, created.Root); err == nil || !strings.Contains(err.Error(), "switch to another checkout") {
		t.Fatalf("current checkout error = %v", err)
	}
	link := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(created.Root, link); err == nil {
		if err := manager.RemoveManaged(link, created.Root); err == nil || !strings.Contains(err.Error(), "switch to another checkout") {
			t.Fatalf("alias checkout error = %v", err)
		}
	}

	// Ignored local files block removal.
	if err := os.WriteFile(filepath.Join(created.Root, ".gitignore"), []byte("*.log\n"), 0o600); err != nil {
		t.Fatalf("write gitignore: %v", err)
	}
	if err := os.WriteFile(filepath.Join(created.Root, "debug.log"), []byte("ignored\n"), 0o600); err != nil {
		t.Fatalf("write ignored file: %v", err)
	}
	if err := manager.RemoveManaged(source, created.Root); err == nil || !strings.Contains(err.Error(), "ignored local files") {
		t.Fatalf("ignored files error = %v", err)
	}
	if err := os.Remove(filepath.Join(created.Root, "debug.log")); err != nil {
		t.Fatalf("remove ignored file: %v", err)
	}
	if err := os.Remove(filepath.Join(created.Root, ".gitignore")); err != nil {
		t.Fatalf("remove gitignore: %v", err)
	}

	// A clean registered checkout is removed and dropped from the list.
	if err := manager.RemoveManaged(source, created.Root); err != nil {
		t.Fatalf("RemoveManaged() error = %v", err)
	}
	if remaining, err := manager.List(source); err != nil || len(remaining) != 0 {
		t.Fatalf("List after RemoveManaged = %#v, %v", remaining, err)
	}
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v failed: %s", args, output)
	}
}
