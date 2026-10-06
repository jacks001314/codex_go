//go:build !windows

package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestForegroundRemoteControlSocketPathOtherLikeRust pins the non-Windows
// branch (Rust cli/src/remote_control_cmd.rs): a private temporary socket
// directory whose cleanup removes the whole directory.
func TestForegroundRemoteControlSocketPathOtherLikeRust(t *testing.T) {
	path, cleanup, err := foregroundRemoteControlSocketPath()
	if err != nil {
		t.Fatalf("foregroundRemoteControlSocketPath() error = %v", err)
	}
	if filepath.Base(path) != "rc.sock" {
		t.Fatalf("socket base name = %q, want rc.sock", filepath.Base(path))
	}
	dir := filepath.Dir(path)
	if !strings.HasPrefix(filepath.Base(dir), "codex-rc-") {
		t.Fatalf("socket directory %q does not use the codex-rc- prefix", dir)
	}
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		t.Fatalf("socket directory %q stat = (%v, %v), want an existing directory", dir, info, err)
	}
	cleanup()
	if _, statErr := os.Stat(dir); !os.IsNotExist(statErr) {
		t.Fatalf("socket directory after cleanup stat error = %v, want not-exist", statErr)
	}
}
