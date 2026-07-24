package session

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestWriterLockRejectsCompetingStoreAndTransfersOwnership(t *testing.T) {
	root := t.TempDir()
	first := NewStore(root)
	second := NewStore(root)
	threadID := ThreadID("thread-owned")

	owner, err := first.AcquireWriter(threadID)
	if err != nil {
		t.Fatalf("AcquireWriter(first) error = %v", err)
	}
	if _, err := second.AcquireWriter(threadID); !errors.Is(err, ErrConflict) {
		t.Fatalf("AcquireWriter(second) error = %v, want ErrConflict", err)
	}
	if err := owner.Close(); err != nil {
		t.Fatalf("owner.Close() error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, writerLockDir, string(threadID)+".lock")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("released lock file stat error = %v, want not exist", err)
	}
	transferred, err := second.AcquireWriter(threadID)
	if err != nil {
		t.Fatalf("AcquireWriter(after release) error = %v", err)
	}
	t.Cleanup(func() { _ = transferred.Close() })
}

func TestAcquireWritersReleasesPartialAcquisitionOnConflict(t *testing.T) {
	root := t.TempDir()
	ownerStore := NewStore(root)
	contender := NewStore(root)
	owned, err := ownerStore.AcquireWriter("thread-b")
	if err != nil {
		t.Fatal(err)
	}
	defer owned.Close()

	if _, err := contender.AcquireWriters([]ThreadID{"thread-a", "thread-b"}); !errors.Is(err, ErrConflict) {
		t.Fatalf("AcquireWriters error = %v, want ErrConflict", err)
	}
	available, err := ownerStore.AcquireWriter("thread-a")
	if err != nil {
		t.Fatalf("partial lock was not released: %v", err)
	}
	defer available.Close()
}
