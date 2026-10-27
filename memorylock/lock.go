// Package memorylock provides the cross-process workspace lock shared by
// external-agent memory imports and consolidation (Rust #56462).
package memorylock

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/gofrs/flock"
)

// ErrBusy reports that another import or consolidation owns the workspace.
// Callers surface it as retryable.
var ErrBusy = errors.New("memory workspace is busy; retry after consolidation or another import finishes")

// Lock keeps a memory workspace exclusively owned until Close.
type Lock struct {
	file *flock.Flock
}

// TryAcquire takes the exclusive workspace lock for memoryRoot without
// blocking. The lock file lives beside the workspace, so baseline resets and
// memory resets cannot replace its inode while another process owns the lock.
func TryAcquire(memoryRoot string) (*Lock, error) {
	memoryRoot = strings.TrimSpace(memoryRoot)
	if memoryRoot == "" {
		return nil, errors.New("memory workspace root is required")
	}
	path := LockPath(memoryRoot)
	lock := flock.New(path)
	locked, err := lock.TryLock()
	if err != nil {
		return nil, fmt.Errorf("lock memory workspace %s: %w", path, err)
	}
	if !locked {
		return nil, ErrBusy
	}
	return &Lock{file: lock}, nil
}

// LockPath is the control file beside a memory workspace root. The file must
// not be removed or replaced while it may be locked.
func LockPath(memoryRoot string) string {
	cleaned := filepath.Clean(memoryRoot)
	return strings.TrimSuffix(cleaned, filepath.Ext(cleaned)) + ".lock"
}

// Close releases the workspace lock.
func (l *Lock) Close() error {
	if l == nil || l.file == nil {
		return nil
	}
	return l.file.Unlock()
}
