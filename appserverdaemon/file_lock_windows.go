//go:build windows

package appserverdaemon

import (
	"errors"
	"fmt"
	"os"
	"time"

	"golang.org/x/sys/windows"
)

type daemonFileLock struct {
	file       *os.File
	locked     bool
	overlapped windows.Overlapped
}

func acquireExclusiveFileLock(path string, timeout time.Duration, retryDelay time.Duration, context string) (*daemonFileLock, error) {
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	if retryDelay <= 0 {
		retryDelay = 50 * time.Millisecond
	}
	deadline := time.Now().Add(timeout)
	for {
		lock, acquired, err := tryAcquireExclusiveFileLock(path, context)
		if err != nil {
			return nil, err
		}
		if acquired {
			return lock, nil
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("timed out waiting for %s %s", context, path)
		}
		time.Sleep(retryDelay)
	}
}

func tryAcquireExclusiveFileLock(path string, context string) (*daemonFileLock, bool, error) {
	file, err := openLockFile(path)
	if err != nil {
		return nil, false, err
	}
	lock := &daemonFileLock{file: file}
	err = windows.LockFileEx(
		windows.Handle(file.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY,
		0,
		1,
		0,
		&lock.overlapped,
	)
	if err != nil {
		_ = file.Close()
		if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
			return nil, false, nil
		}
		if context == "" {
			context = "file lock"
		}
		return nil, false, fmt.Errorf("failed to lock %s: %w", context, err)
	}
	lock.locked = true
	return lock, true, nil
}

func fileLockIsActive(path string) (bool, error) {
	lock, acquired, err := tryAcquireExclusiveFileLock(path, "file lock")
	if err != nil {
		return false, err
	}
	if acquired {
		_ = lock.Close()
		return false, nil
	}
	return true, nil
}

func (l *daemonFileLock) Close() error {
	if l == nil || l.file == nil {
		return nil
	}
	var unlockErr error
	if l.locked {
		unlockErr = windows.UnlockFileEx(windows.Handle(l.file.Fd()), 0, 1, 0, &l.overlapped)
		l.locked = false
	}
	closeErr := l.file.Close()
	if unlockErr != nil {
		return unlockErr
	}
	return closeErr
}
