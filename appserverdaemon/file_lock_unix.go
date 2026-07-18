//go:build !windows

package appserverdaemon

import (
	"errors"
	"fmt"
	"os"
	"syscall"
	"time"
)

type daemonFileLock struct {
	file *os.File
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
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = file.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, false, nil
		}
		if context == "" {
			context = "file lock"
		}
		return nil, false, fmt.Errorf("failed to lock %s: %w", context, err)
	}
	return &daemonFileLock{file: file}, true, nil
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
	err := syscall.Flock(int(l.file.Fd()), syscall.LOCK_UN)
	closeErr := l.file.Close()
	if err != nil {
		return err
	}
	return closeErr
}
