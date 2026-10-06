//go:build !windows

package appserverdaemon

import (
	"context"
	"strings"
	"testing"
	"time"
)

// TestRunInstallerProcessBoundsStderrDrainLikeRust covers Rust #50499's bounded
// drain: a descendant that keeps the stderr pipe open must not delay error
// reporting past the drain timeout, and the captured tail still reaches the
// error.
func TestRunInstallerProcessBoundsStderrDrainLikeRust(t *testing.T) {
	// The backgrounded sleep inherits stderr, so the pipe stays open after the
	// script itself exits.
	script := []byte("(sleep 5) &\necho installer-boom >&2\nexit 1\n")
	start := time.Now()
	err := runInstallerProcess(context.Background(), script, nil)
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("runInstallerProcess() succeeded, want a failure")
	}
	if elapsed > 3*time.Second {
		t.Fatalf("drain took %s, want it bounded near %s", elapsed, installerStderrDrainTimeout)
	}
	if !strings.Contains(err.Error(), "installer-boom") {
		t.Fatalf("error %q does not carry the captured stderr tail", err)
	}
}
