package auth

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gofrs/flock"
)

// authCommitLockTimeout bounds how long a credential commit waits for another
// process before failing (Rust #58251 CommittedStorage::lock).
const authCommitLockTimeout = 5 * time.Second

// authCommitMu serializes credential commits inside this process before the
// cross-process sidecar lock is taken.
var authCommitMu sync.Mutex

// authCommitGuard holds the "<auth file>.lock" sidecar for one commit.
type authCommitGuard struct {
	lock *flock.Flock
	held bool
}

// lockCommit takes the credential commit lock. File, keyring, and auto writers
// all coordinate through the same sidecar because auto may use the file
// fallback (Rust #58251).
func (s *Store) lockCommit() (*authCommitGuard, error) {
	if s == nil || s.options.Mode == AuthCredentialsStoreEphemeral {
		return &authCommitGuard{}, nil
	}
	authCommitMu.Lock()
	path, err := s.commitLockPath()
	if err != nil {
		authCommitMu.Unlock()
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		authCommitMu.Unlock()
		return nil, err
	}
	lock := flock.New(path)
	deadline := time.Now().Add(authCommitLockTimeout)
	for {
		locked, err := lock.TryLock()
		if err != nil {
			authCommitMu.Unlock()
			return nil, err
		}
		if locked {
			return &authCommitGuard{lock: lock, held: true}, nil
		}
		if time.Now().After(deadline) {
			authCommitMu.Unlock()
			return nil, errors.New("credential commit lock timed out")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// commitLockPath resolves the credential symlink chain so aliases of auth.json
// share the lock of the file they write (Rust #58251).
func (s *Store) commitLockPath() (string, error) {
	resolved, err := resolveAuthFile(s.Path())
	if err != nil {
		return "", err
	}
	return strings.TrimSuffix(resolved, filepath.Ext(resolved)) + ".json.lock", nil
}

func (g *authCommitGuard) release() {
	if g == nil {
		return
	}
	if g.lock != nil {
		_ = g.lock.Unlock()
	}
	if g.held {
		authCommitMu.Unlock()
	}
}
