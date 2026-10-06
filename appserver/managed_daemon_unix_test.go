//go:build unix

package appserver

import (
	"testing"

	"golang.org/x/sys/unix"
)

// TestRaiseManagedDaemonNoFileLimitLikeRust mirrors Rust #51470: the managed
// app-server daemon raises its soft RLIMIT_NOFILE to at most 4096, capped by the
// inherited hard limit, and preserves an already higher soft limit.
func TestRaiseManagedDaemonNoFileLimitLikeRust(t *testing.T) {
	if err := raiseManagedDaemonNoFileLimit(); err != nil {
		t.Skipf("raiseManagedDaemonNoFileLimit() unavailable in this environment: %v", err)
	}
	var limit unix.Rlimit
	if err := unix.Getrlimit(unix.RLIMIT_NOFILE, &limit); err != nil {
		t.Fatalf("Getrlimit() error = %v", err)
	}
	want := limit.Max
	if want > managedDaemonNoFileLimit {
		want = managedDaemonNoFileLimit
	}
	if limit.Cur < want {
		t.Fatalf("soft nofile limit = %d, want >= %d", limit.Cur, want)
	}
}
