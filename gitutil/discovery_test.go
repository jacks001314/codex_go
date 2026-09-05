package gitutil

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestDiscoverGitRootReturnsRepositoryRoot(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	root := t.TempDir()
	runGitDiscoveryTest(t, root, "init")
	runGitDiscoveryTest(t, root, "config", "user.email", "test@example.com")
	runGitDiscoveryTest(t, root, "config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(root, "file"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGitDiscoveryTest(t, root, "add", "file")
	runGitDiscoveryTest(t, root, "commit", "-m", "init")

	got, err := DiscoverGitRoot(context.Background(), root)
	if err != nil {
		t.Fatalf("DiscoverGitRoot() error = %v", err)
	}
	if filepath.Clean(got) != filepath.Clean(root) {
		t.Fatalf("DiscoverGitRoot() = %q, want %q", got, root)
	}
}

func TestDiscoverGitRootHonorsCallerCancellation(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := DiscoverGitRoot(ctx, t.TempDir()); err == nil {
		t.Fatal("cancelled DiscoverGitRoot returned nil error")
	}
	time.Sleep(0)
}

func runGitDiscoveryTest(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v failed: %s", args, output)
	}
}
