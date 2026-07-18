//go:build windows

package appserverdaemon

import (
	"os"
	"time"
)

type daemonFileLock struct {
	file *os.File
}

func acquireExclusiveFileLock(path string, _ time.Duration, _ time.Duration, _ string) (*daemonFileLock, error) {
	file, err := openLockFile(path)
	if err != nil {
		return nil, err
	}
	return &daemonFileLock{file: file}, nil
}

func tryAcquireExclusiveFileLock(path string, _ string) (*daemonFileLock, bool, error) {
	file, err := openLockFile(path)
	if err != nil {
		return nil, false, err
	}
	return &daemonFileLock{file: file}, true, nil
}

func fileLockIsActive(_ string) (bool, error) {
	return false, nil
}

func (l *daemonFileLock) Close() error {
	if l == nil || l.file == nil {
		return nil
	}
	return l.file.Close()
}
