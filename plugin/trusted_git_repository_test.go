package plugin

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestTrustedGitRepositoryOwnerAndReclamation(t *testing.T) {
	home := t.TempDir()
	command := exec.Command("git")
	live, err := configureTrustedGitRepository(command, home)
	if err != nil {
		t.Fatal(err)
	}
	defer live.Close()
	stale, err := configureTrustedGitRepository(exec.Command("git"), home)
	if err != nil {
		t.Fatal(err)
	}
	// Simulate process exit releasing the OS lock without directory cleanup.
	if err := stale.lock.Unlock(); err != nil {
		t.Fatal(err)
	}
	if err := stale.lock.Close(); err != nil {
		t.Fatal(err)
	}
	stale.lock = nil
	defer stale.Close()
	next, err := configureTrustedGitRepository(exec.Command("git"), home)
	if err != nil {
		t.Fatal(err)
	}
	defer next.Close()
	if _, err := os.Stat(stale.Path()); !os.IsNotExist(err) {
		t.Fatalf("abandoned repository remains: %v", err)
	}
	cleanupMarketplaceStaging(filepath.Join(home, ".tmp"), time.Now())
	for _, repo := range []*MarketplaceStagingDir{live, next} {
		data, err := os.ReadFile(filepath.Join(repo.Path(), "HEAD"))
		if err != nil || string(data) != "ref: refs/heads/main\n" {
			t.Fatalf("live repository damaged: %q %v", data, err)
		}
	}
}

func TestTrustedGitRejectsSymlinkedStaging(t *testing.T) {
	home := t.TempDir()
	outside := t.TempDir()
	root := filepath.Join(home, ".tmp")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, marketplaceStagingDirName)); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	guard, err := configureTrustedGitRepository(exec.Command("git"), home)
	if guard != nil {
		guard.Close()
		t.Fatal("accepted symlink staging")
	}
	if err == nil || !strings.Contains(err.Error(), "staging directory is a symlink") {
		t.Fatalf("unexpected error: %v", err)
	}
	entries, err := os.ReadDir(outside)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("outside target modified: %v", entries)
	}
}
