package session

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"

	"github.com/gofrs/flock"
)

const writerLockDir = "thread-writer-locks"

type WriterLock struct {
	coordinator *writerLockCoordinator
	path        string
	lock        *flock.Flock
	once        sync.Once
}

type writerLockCoordinator struct {
	directory    string
	coordination *flock.Flock
}

func newWriterLockCoordinator(root string) *writerLockCoordinator {
	directory := filepath.Join(root, writerLockDir)
	return &writerLockCoordinator{
		directory:    directory,
		coordination: flock.New(filepath.Join(directory, ".coordination.lock")),
	}
}

func (s *Store) AcquireWriter(threadID ThreadID) (*WriterLock, error) {
	if s == nil {
		return nil, fmt.Errorf("thread store is nil")
	}
	if err := validateThreadID(threadID); err != nil {
		return nil, err
	}
	return s.writerLocks().acquire(threadID)
}

func (s *Store) AcquireWriters(threadIDs []ThreadID) ([]*WriterLock, error) {
	if s == nil {
		return nil, fmt.Errorf("thread store is nil")
	}
	ids := append([]ThreadID(nil), threadIDs...)
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	ids = dedupeThreadIDs(ids)
	locks := make([]*WriterLock, 0, len(ids))
	for _, threadID := range ids {
		lock, err := s.AcquireWriter(threadID)
		if err != nil {
			releaseWriterLocks(locks)
			return nil, err
		}
		locks = append(locks, lock)
	}
	return locks, nil
}

func (s *Store) writerLocks() *writerLockCoordinator {
	s.writerLocksOnce.Do(func() {
		s.writerLockCoordinator = newWriterLockCoordinator(s.root)
	})
	return s.writerLockCoordinator
}

func (c *writerLockCoordinator) acquire(threadID ThreadID) (*WriterLock, error) {
	if err := os.MkdirAll(c.directory, 0o700); err != nil {
		return nil, fmt.Errorf("create thread writer lock directory: %w", err)
	}
	if err := c.coordination.Lock(); err != nil {
		return nil, fmt.Errorf("acquire thread writer coordination lock: %w", err)
	}
	defer func() { _ = c.coordination.Unlock() }()

	path := filepath.Join(c.directory, string(threadID)+".lock")
	fileLock := flock.New(path)
	acquired, err := fileLock.TryLock()
	if err != nil {
		return nil, fmt.Errorf("acquire thread writer lock %s: %w", path, err)
	}
	if !acquired {
		return nil, fmt.Errorf("%w: thread %s already has an active writer", ErrConflict, threadID)
	}
	return &WriterLock{coordinator: c, path: path, lock: fileLock}, nil
}

func (l *WriterLock) Close() error {
	if l == nil {
		return nil
	}
	var closeErr error
	l.once.Do(func() {
		if err := l.coordinator.coordination.Lock(); err != nil {
			closeErr = fmt.Errorf("acquire thread writer coordination lock: %w", err)
			return
		}
		defer func() {
			if err := l.coordinator.coordination.Unlock(); closeErr == nil && err != nil {
				closeErr = err
			}
		}()
		if err := l.lock.Unlock(); err != nil {
			closeErr = err
			return
		}
		if err := os.Remove(l.path); err != nil && !errors.Is(err, os.ErrNotExist) {
			closeErr = err
		}
	})
	return closeErr
}

func releaseWriterLocks(locks []*WriterLock) {
	for i := len(locks) - 1; i >= 0; i-- {
		_ = locks[i].Close()
	}
}

func dedupeThreadIDs(ids []ThreadID) []ThreadID {
	if len(ids) < 2 {
		return ids
	}
	out := ids[:1]
	for _, id := range ids[1:] {
		if id != out[len(out)-1] {
			out = append(out, id)
		}
	}
	return out
}
