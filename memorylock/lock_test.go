package memorylock

import (
	"errors"
	"path/filepath"
	"testing"
)

func TestTryAcquireSerializesWorkspaceOwners(t *testing.T) {
	root := filepath.Join(t.TempDir(), "memories_v2")
	first, err := TryAcquire(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := TryAcquire(root); !errors.Is(err, ErrBusy) {
		t.Fatalf("second acquire error = %v, want ErrBusy", err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	second, err := TryAcquire(root)
	if err != nil {
		t.Fatalf("acquire after release error = %v", err)
	}
	if err := second.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestLockPathLivesBesideWorkspace(t *testing.T) {
	if got, want := LockPath(filepath.Join("home", "memories_v2")), filepath.Join("home", "memories_v2.lock"); got != want {
		t.Fatalf("LockPath = %q, want %q", got, want)
	}
	if got, want := LockPath(filepath.Join("home", ".codex", "memories")), filepath.Join("home", ".codex", "memories.lock"); got != want {
		t.Fatalf("LockPath = %q, want %q", got, want)
	}
}
