package sandbox

import (
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// TestProbeSystemBwrapDoesNotWaitForDescendantHoldingStderr pins Rust #62160:
// the launcher exits immediately but a descendant keeps the inherited stderr
// pipe open, so exit detection must not wait on the pipe.
func TestProbeSystemBwrapDoesNotWaitForDescendantHoldingStderr(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("bwrap is Linux-only")
	}
	path := filepath.Join(t.TempDir(), "bwrap")
	writeFakeBwrap(t, path, "echo 'bwrap: loopback: Failed RTM_NEWADDR' >&2\nsleep 5 &\nexit 1\n")
	started := time.Now()
	usable := probeSystemBwrapUserNamespaces(path, 500*time.Millisecond)
	elapsed := time.Since(started)
	if usable {
		t.Fatal("probe reported a user-namespace failure as usable")
	}
	if elapsed > 2*time.Second {
		t.Fatalf("probe took %s, want the verdict without waiting for the descendant", elapsed)
	}
}

func TestProbeSystemBwrapBoundsCapturedStderr(t *testing.T) {
	snapshot := &bwrapStderrSnapshot{limit: 8}
	snapshot.append([]byte("0123456789"))
	snapshot.append([]byte("later"))
	if got := snapshot.String(); got != "01234567" {
		t.Fatalf("snapshot = %q, want the bounded prefix", got)
	}
}

func TestProbeSystemBwrapTreatsUnstartableLauncherAsUsable(t *testing.T) {
	if !probeSystemBwrapUserNamespaces(filepath.Join(t.TempDir(), "missing-bwrap"), time.Second) {
		t.Fatal("a launcher that cannot start must be treated as usable")
	}
}
