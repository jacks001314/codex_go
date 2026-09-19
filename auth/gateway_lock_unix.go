//go:build !windows

package auth

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

// gatewayCredentialLock is the held sidecar lock. Rust keeps the file open for
// the lifetime of the guard, which releases it on drop.
type gatewayCredentialLock struct {
	file *os.File
}

func tryAcquireGatewayCredentialLock(path string) (*gatewayCredentialLock, bool, error) {
	file, err := openGatewayCredentialLockFile(path)
	if err != nil {
		return nil, false, err
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = file.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("failed to lock provider OAuth credentials: %w", err)
	}
	return &gatewayCredentialLock{file: file}, true, nil
}

func (l *gatewayCredentialLock) release() error {
	if l == nil || l.file == nil {
		return nil
	}
	unlockErr := syscall.Flock(int(l.file.Fd()), syscall.LOCK_UN)
	closeErr := l.file.Close()
	l.file = nil
	if unlockErr != nil {
		return unlockErr
	}
	return closeErr
}
