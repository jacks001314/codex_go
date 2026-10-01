//go:build !windows

package codexuds

import (
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
)

// socketDirectoryMode keeps the rendezvous directory owner-only: owner
// traversal and socket creation stay possible while group and other access are
// denied (Rust SOCKET_DIR_MODE).
const socketDirectoryMode = 0o700

// checkSocketPath accepts the platform's addressable length. Unix sun_path is
// also bounded, but Go's runtime reports that limit when the socket is bound.
func checkSocketPath(string) error {
	return nil
}

// preparePrivateSocketDirectory creates socketDir with mode 0700 and repairs an
// existing directory whose permissions are not exactly 0700 (Rust
// uds::platform::prepare_private_socket_directory).
func preparePrivateSocketDirectory(socketDir string) error {
	socketDir = strings.TrimSpace(socketDir)
	if socketDir == "" {
		return errors.New("socket directory is empty")
	}
	if err := os.MkdirAll(socketDir, socketDirectoryMode); err != nil {
		return err
	}
	info, err := os.Lstat(socketDir)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return errors.New("socket directory path exists and is not a directory: " + socketDir)
	}
	if info.Mode().Perm() != socketDirectoryMode {
		if err := os.Chmod(socketDir, socketDirectoryMode); err != nil {
			return err
		}
	}
	return nil
}

// validatePrivateSocketPath resolves and returns the socket path, leaving the
// directory checks to the 0700 mode applied at bind time.
func validatePrivateSocketPath(socketPath string) (string, *os.File, error) {
	absolute, err := filepath.Abs(strings.TrimSpace(socketPath))
	if err != nil {
		return "", nil, err
	}
	return absolute, nil, nil
}

// prepareControlSocketPath removes a stale socket left by a crashed daemon.
func prepareControlSocketPath(socketPath string) error {
	if err := os.Remove(socketPath); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// ensureNonElevatedPeer is a no-op where the socket path itself identifies the
// peer.
func ensureNonElevatedPeer(net.Conn) error {
	return nil
}
