package sandbox

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
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

// TestDrainBwrapProbeStderrKeepsLauncherOutput pins the ordering bug behind the
// flaky probe verdict: the launcher writes its failure line and exits while a
// descendant keeps the write end open, so the snapshot must be taken after the
// reader consumed the buffered bytes. Reading it before the reader is done can
// see an empty snapshot and call a broken launcher usable.
func TestDrainBwrapProbeStderrKeepsLauncherOutput(t *testing.T) {
	readEnd, writeEnd, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe() error = %v", err)
	}
	defer writeEnd.Close()
	snapshot := &bwrapStderrSnapshot{limit: 1024}
	readerDone := make(chan struct{})
	go func() {
		defer close(readerDone)
		snapshot.collect(readEnd)
	}()
	// A descendant would keep the write end open; this test never closes it until
	// the function returns, which is exactly the condition that used to hang or
	// lose data.
	if _, err := writeEnd.WriteString("bwrap: loopback: Failed RTM_NEWADDR\n"); err != nil {
		t.Fatalf("write stderr = %v", err)
	}
	started := time.Now()
	got := drainBwrapProbeStderr(readEnd, readerDone, snapshot)
	elapsed := time.Since(started)
	if !strings.Contains(got, "Failed RTM_NEWADDR") {
		t.Fatalf("captured stderr = %q, want the launcher failure line", got)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("drain took %s, want it to stop without waiting for the descendant", elapsed)
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
