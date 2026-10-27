package session

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gofrs/flock"
)

// Rust #57360: coordination contention must time out instead of blocking
// writer cleanup forever, and release once the holder lets go.
func TestWriterLockTimesOutWhileCoordinationHeld(t *testing.T) {
	root := t.TempDir()
	store := NewStore(root)
	coordinator := newWriterLockCoordinator(root)
	if err := os.MkdirAll(coordinator.directory, 0o700); err != nil {
		t.Fatalf("mkdir lock dir: %v", err)
	}
	held := flock.New(filepath.Join(coordinator.directory, ".coordination.lock"))
	locked, err := held.TryLock()
	if err != nil || !locked {
		t.Fatalf("hold coordination lock: locked=%v err=%v", locked, err)
	}
	defer func() { _ = held.Unlock() }()

	previous := writerLockCoordinationTimeout
	writerLockCoordinationTimeout = 100 * time.Millisecond
	defer func() { writerLockCoordinationTimeout = previous }()

	if _, err := store.AcquireWriter("thread-timeout"); !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("AcquireWriter() error = %v, want deadline exceeded", err)
	}
	if err := held.Unlock(); err != nil {
		t.Fatalf("release coordination lock: %v", err)
	}
	lock, err := store.AcquireWriter("thread-timeout")
	if err != nil {
		t.Fatalf("AcquireWriter() after release error = %v", err)
	}
	if err := lock.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
}
