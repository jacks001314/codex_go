package rollout

import (
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/gofrs/flock"
)

// rolloutMaintenanceLockFile mirrors Rust maintenance.rs
// ROLLOUT_MAINTENANCE_LOCK.
const rolloutMaintenanceLockFile = "rollout-maintenance.lock"

// RolloutMaintenanceGuard holds exclusive ownership of operations that
// replace local rollout files (rollout compression and legacy migration both
// publish by renaming a replacement over an existing rollout path). Mirrors
// Rust RolloutMaintenanceGuard (rollout/src/maintenance.rs).
type RolloutMaintenanceGuard struct {
	lock *flock.Flock
}

// TryAcquireRolloutMaintenanceLock mirrors Rust
// try_acquire_rollout_maintenance_lock: try to exclude rollout compression
// and migration for one Codex home. Returns nil when another maintenance job
// holds the lock (WouldBlock), an error only on real I/O failures.
func TryAcquireRolloutMaintenanceLock(codexHome string) (*RolloutMaintenanceGuard, error) {
	directory := filepath.Join(codexHome, ".tmp")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return nil, err
	}
	lock := flock.New(filepath.Join(directory, rolloutMaintenanceLockFile))
	locked, err := lock.TryLock()
	if err != nil {
		return nil, err
	}
	if !locked {
		return nil, nil
	}
	return &RolloutMaintenanceGuard{lock: lock}, nil
}

// Release unlocks the maintenance lock. Safe to call on a nil guard.
func (g *RolloutMaintenanceGuard) Release() {
	if g == nil || g.lock == nil {
		return
	}
	_ = g.lock.Unlock()
}

// WaitAcquireRolloutMaintenanceLock blocks until the lock is available or the
// deadline passes, returning nil when acquired. Used by tests and callers that
// prefer to wait instead of skipping maintenance.
func WaitAcquireRolloutMaintenanceLock(codexHome string, timeout time.Duration) (*RolloutMaintenanceGuard, error) {
	deadline := time.Now().Add(timeout)
	for {
		guard, err := TryAcquireRolloutMaintenanceLock(codexHome)
		if err != nil {
			return nil, err
		}
		if guard != nil {
			return guard, nil
		}
		if time.Now().After(deadline) {
			return nil, errors.New("rollout maintenance lock not acquired before timeout")
		}
		time.Sleep(25 * time.Millisecond)
	}
}
