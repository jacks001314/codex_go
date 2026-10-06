//go:build windows

package app

import (
	"fmt"
	"os"
	"path/filepath"
)

// foregroundRemoteControlSocketPath selects the foreground remote-control
// control-socket path on Windows (Rust #50700,
// cli/src/remote_control_cmd.rs). The socket parent is left for the transport,
// which creates it with a protected, user-only DACL, instead of pre-creating a
// temporary directory that inherits the broader `%TEMP%` ACL. The path is
// stable and scoped to this process; cleanup removes only this instance's
// socket file, never the shared parent directory.
func foregroundRemoteControlSocketPath() (string, func(), error) {
	path := filepath.Join(os.TempDir(), "codex-remote-control", fmt.Sprintf("rc-%d.sock", os.Getpid()))
	return path, func() { _ = os.Remove(path) }, nil
}
