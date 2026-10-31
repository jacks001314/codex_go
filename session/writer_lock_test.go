package session

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
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

// Rust parity: codex-rs/rollout/src/writer_lock_tests.rs at #44138. Publication
// skips a live writer, holds coordination until it is released, and treats an
// existing-but-unlocked lock file as idle.
func TestTryAcquireForPublicationSkipsLiveWriterAndHoldsCoordination(t *testing.T) {
	root := t.TempDir()
	publisher := NewStore(root)
	writer := NewStore(root)
	threadID := ThreadID("thread-publication")

	owner, err := writer.AcquireWriter(threadID)
	if err != nil {
		t.Fatalf("AcquireWriter(owner) error = %v", err)
	}
	if _, err := publisher.TryAcquireForPublication(threadID); !errors.Is(err, ErrConflict) {
		t.Fatalf("TryAcquireForPublication(busy) error = %v, want ErrConflict", err)
	}
	if err := owner.Close(); err != nil {
		t.Fatalf("owner.Close() error = %v", err)
	}

	publication, err := publisher.TryAcquireForPublication(threadID)
	if err != nil {
		t.Fatalf("TryAcquireForPublication(idle) error = %v", err)
	}
	previousTimeout := writerLockCoordinationTimeout
	writerLockCoordinationTimeout = 100 * time.Millisecond
	defer func() { writerLockCoordinationTimeout = previousTimeout }()
	if _, err := writer.AcquireWriter(ThreadID("thread-other")); !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("AcquireWriter during publication error = %v, want timeout", err)
	}
	if err := publication.Release(); err != nil {
		t.Fatalf("publication.Release() error = %v", err)
	}
	other, err := writer.AcquireWriter(ThreadID("thread-other"))
	if err != nil {
		t.Fatalf("AcquireWriter after release error = %v", err)
	}
	defer other.Close()

	stalePath := filepath.Join(root, writerLockDir, string(threadID)+".lock")
	if err := os.WriteFile(stalePath, nil, 0o600); err != nil {
		t.Fatalf("WriteFile(stale lock) error = %v", err)
	}
	stale, err := publisher.TryAcquireForPublication(threadID)
	if err != nil {
		t.Fatalf("TryAcquireForPublication(stale) error = %v, want idle", err)
	}
	if err := stale.Release(); err != nil {
		t.Fatalf("stale.Release() error = %v", err)
	}
	reowned, err := writer.AcquireWriter(threadID)
	if err != nil {
		t.Fatalf("AcquireWriter(stale path) error = %v", err)
	}
	defer reowned.Close()
}

// Publication probes read an existing lock file only: a thread that never owned
// a writer must not gain a lock file just because its rollout was compressed.
func TestTryAcquireForPublicationDoesNotCreateThreadLockFile(t *testing.T) {
	root := t.TempDir()
	store := NewStore(root)
	publication, err := store.TryAcquireForPublication(ThreadID("thread-never-locked"))
	if err != nil {
		t.Fatalf("TryAcquireForPublication error = %v", err)
	}
	if err := publication.Release(); err != nil {
		t.Fatalf("Release error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, writerLockDir, "thread-never-locked.lock")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("publication probe created a thread lock file: %v", err)
	}
}
