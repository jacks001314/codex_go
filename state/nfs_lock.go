package state

import "github.com/gofrs/flock"

// fileLock is the migration directory lock shared by startup and the CLI.
type fileLock struct {
	flock *flock.Flock
}

// tryLockFile attempts a non-blocking exclusive lock. It returns (nil, nil)
// when another process holds the lock.
func tryLockFile(path string) (*fileLock, error) {
	lock := flock.New(path)
	locked, err := lock.TryLock()
	if err != nil {
		return nil, err
	}
	if !locked {
		return nil, nil
	}
	return &fileLock{flock: lock}, nil
}

func (l *fileLock) Unlock() error {
	if l == nil || l.flock == nil {
		return nil
	}
	return l.flock.Unlock()
}
