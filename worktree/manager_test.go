package worktree

import (
	"os"
	"os/exec"
	"path/filepath"
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

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v failed: %s", args, output)
	}
}
