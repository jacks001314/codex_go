package session

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/gofrs/flock"
)

// TestWriterLockCloseDoesNotBlockOnHeldCoordination pins Rust #59969: cleanup
// takes a shared coordination lock without waiting, so releasing a writer while
// another process holds coordination stays instant instead of queueing on the
// 50 ms retry budget for the whole 30 s window. Reverting Close to an exclusive
// acquire makes this test take the full hold time and fail.
func TestWriterLockCloseDoesNotBlockOnHeldCoordination(t *testing.T) {
	root := t.TempDir()
	store := NewStore(root)
	lock, err := store.AcquireWriter("thread-close")
	if err != nil {
		t.Fatalf("AcquireWriter() error = %v", err)
	}

	coordinator := newWriterLockCoordinator(root)
	held := flock.New(filepath.Join(coordinator.directory, ".coordination.lock"))
	locked, err := held.TryLock()
	if err != nil || !locked {
		t.Fatalf("hold coordination lock: locked=%v err=%v", locked, err)
	}
	const hold = 300 * time.Millisecond
	released := make(chan struct{})
	go func() {
		time.Sleep(hold)
		_ = held.Unlock()
		close(released)
	}()

	started := time.Now()
	closeErr := lock.Close()
	elapsed := time.Since(started)
	<-released
	if closeErr != nil {
		t.Fatalf("Close() error = %v", closeErr)
	}
	if elapsed > hold/2 {
		t.Fatalf("Close() took %v while coordination was held for %v, want a non-blocking close", elapsed, hold)
	}

	// The skipped removal leaves the file behind; the next writer must still own
	// the thread once coordination is free.
	again, err := store.AcquireWriter("thread-close")
	if err != nil {
		t.Fatalf("AcquireWriter() after a contended close error = %v", err)
	}
	if err := again.Close(); err != nil {
		t.Fatalf("second Close() error = %v", err)
	}
}
