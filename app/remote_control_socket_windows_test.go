//go:build windows

package app

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// TestForegroundRemoteControlSocketPathWindowsLikeRust mirrors Rust #50700: on
// Windows the transport creates the socket parent with a protected DACL, so the
// foreground remote control uses a stable per-process path under the temp
// directory instead of a pre-created temporary directory.
func TestForegroundRemoteControlSocketPathWindowsLikeRust(t *testing.T) {
	path, cleanup, err := foregroundRemoteControlSocketPath()
	if err != nil {
		t.Fatalf("foregroundRemoteControlSocketPath() error = %v", err)
	}
	want := filepath.Join(os.TempDir(), "codex-remote-control", fmt.Sprintf("rc-%d.sock", os.Getpid()))
	if path != want {
		t.Fatalf("socket path = %q, want %q", path, want)
	}
	// Cleanup removes only this instance's socket file and never the shared
	// parent directory.
	parent := filepath.Dir(path)
	_, parentErr := os.Stat(parent)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		t.Fatalf("MkdirAll(%q) error = %v", parent, err)
	}
	defer func() {
		// Only remove the parent when this test created it, so a real
		// remote-control socket directory is never disturbed.
		if os.IsNotExist(parentErr) {
			_ = os.Remove(parent)
		}
	}()
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatalf("WriteFile(%q) error = %v", path, err)
	}
	cleanup()
	if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
		t.Fatalf("socket file after cleanup stat error = %v, want not-exist", statErr)
	}
	if _, statErr := os.Stat(parent); statErr != nil {
		t.Fatalf("shared socket parent removed by cleanup: %v", statErr)
	}
}
