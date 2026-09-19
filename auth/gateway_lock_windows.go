//go:build windows

package auth

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/windows"
)

// gatewayCredentialLock is the held sidecar lock. Rust keeps the file open for
// the lifetime of the guard, which releases it on drop.
type gatewayCredentialLock struct {
	file       *os.File
	overlapped windows.Overlapped
}

func tryAcquireGatewayCredentialLock(path string) (*gatewayCredentialLock, bool, error) {
	file, err := openGatewayCredentialLockFile(path)
	if err != nil {
		return nil, false, err
	}
	lock := &gatewayCredentialLock{file: file}
	if info, statErr := file.Stat(); statErr == nil && info.Size() == 0 {
		// LockFileEx cannot lock a zero-length range on an empty file.
		_, _ = file.WriteString("\n")
	}
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
		return nil, false, fmt.Errorf("failed to lock provider OAuth credentials: %w", err)
	}
	return lock, true, nil
}

func (l *gatewayCredentialLock) release() error {
	if l == nil || l.file == nil {
		return nil
	}
	file := l.file
	l.file = nil
	unlockErr := windows.UnlockFileEx(windows.Handle(file.Fd()), 0, 1, 0, &l.overlapped)
	closeErr := file.Close()
	if unlockErr != nil {
		return unlockErr
	}
	return closeErr
}
