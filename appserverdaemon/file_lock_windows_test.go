//go:build windows

package appserverdaemon

import (
	"path/filepath"
	"testing"
	"time"
)

func TestWindowsFileLockExcludesSecondOwner(t *testing.T) {
	path := filepath.Join(t.TempDir(), "daemon.lock")
	first, acquired, err := tryAcquireExclusiveFileLock(path, "test lock")
	if err != nil || !acquired {
		t.Fatalf("first lock acquired=%v err=%v", acquired, err)
	}
	defer first.Close()

	second, acquired, err := tryAcquireExclusiveFileLock(path, "test lock")
	if err != nil {
		t.Fatalf("second tryAcquire error = %v", err)
	}
	if acquired {
		_ = second.Close()
		t.Fatalf("second lock unexpectedly acquired while first is held")
	}

	active, err := fileLockIsActive(path)
	if err != nil || !active {
		t.Fatalf("fileLockIsActive while held = %v, %v; want true, nil", active, err)
	}

	if err := first.Close(); err != nil {
		t.Fatalf("first Close error = %v", err)
	}
	active, err = fileLockIsActive(path)
	if err != nil || active {
		t.Fatalf("fileLockIsActive after release = %v, %v; want false, nil", active, err)
	}
}

func TestWindowsFileLockBlockingAcquireWaitsForRelease(t *testing.T) {
	path := filepath.Join(t.TempDir(), "daemon-blocking.lock")
	first, acquired, err := tryAcquireExclusiveFileLock(path, "blocking test")
	if err != nil || !acquired {
		t.Fatalf("first lock acquired=%v err=%v", acquired, err)
	}
	done := make(chan *daemonFileLock, 1)
	errs := make(chan error, 1)
	go func() {
		lock, err := acquireExclusiveFileLock(path, 2*time.Second, 10*time.Millisecond, "blocking test")
		if err != nil {
			errs <- err
			return
		}
		done <- lock
	}()
	if err := first.Close(); err != nil {
		t.Fatalf("releasing first lock error = %v", err)
	}
	select {
	case lock := <-done:
		lock.Close()
	case err := <-errs:
		t.Fatalf("blocking acquire error = %v", err)
	}
}
