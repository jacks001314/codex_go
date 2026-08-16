package rollout

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRolloutMaintenanceLockExcludesConcurrentJobsLikeRust(t *testing.T) {
	// Mirrors Rust try_acquire_rollout_maintenance_lock: the second acquirer
	// gets nil (WouldBlock) while the first holds the lock.
	home := t.TempDir()
	first, err := TryAcquireRolloutMaintenanceLock(home)
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	if first == nil {
		t.Fatal("first acquire returned nil guard")
	}
	second, err := TryAcquireRolloutMaintenanceLock(home)
	if err != nil {
		t.Fatalf("second acquire: %v", err)
	}
	if second != nil {
		t.Fatal("second acquire succeeded while first held the lock")
	}
	first.Release()
	third, err := TryAcquireRolloutMaintenanceLock(home)
	if err != nil {
		t.Fatalf("third acquire: %v", err)
	}
	if third == nil {
		t.Fatal("third acquire returned nil after release")
	}
	third.Release()

	// The lock file lives under .tmp/rollout-maintenance.lock.
	lockPath := filepath.Join(home, ".tmp", rolloutMaintenanceLockFile)
	if _, err := os.Stat(lockPath); err != nil {
		t.Fatalf("lock file missing: %v", err)
	}
}

func TestWaitAcquireRolloutMaintenanceLockTimesOut(t *testing.T) {
	home := t.TempDir()
	first, err := TryAcquireRolloutMaintenanceLock(home)
	if err != nil || first == nil {
		t.Fatalf("first acquire err=%v", err)
	}
	defer first.Release()
	start := time.Now()
	guard, err := WaitAcquireRolloutMaintenanceLock(home, 60*time.Millisecond)
	if err == nil {
		if guard != nil {
			guard.Release()
		}
		t.Fatal("WaitAcquire succeeded while lock was held")
	}
	if elapsed := time.Since(start); elapsed < 55*time.Millisecond {
		t.Fatalf("WaitAcquire returned too fast: %v", elapsed)
	}
}
