//go:build !windows

package app

import (
	"fmt"
	"os"
	"path/filepath"
)

// foregroundRemoteControlSocketPath selects the foreground remote-control
// control-socket path (Rust cli/src/remote_control_cmd.rs). Non-Windows keeps
// the private temporary socket directory created under /tmp, falling back to
// the platform temp directory, and returns a cleanup that removes it.
func foregroundRemoteControlSocketPath() (string, func(), error) {
	dir, err := os.MkdirTemp("/tmp", "codex-rc-")
	if err != nil {
		dir, err = os.MkdirTemp("", "codex-rc-")
	}
	if err != nil {
		return "", nil, fmt.Errorf("failed to create private app-server socket directory: %w", err)
	}
	return filepath.Join(dir, "rc.sock"), func() { _ = os.RemoveAll(dir) }, nil
}
