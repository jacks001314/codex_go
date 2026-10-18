//go:build !windows

package codexuds

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// socketDirectoryMode keeps the rendezvous directory owner-only: owner
// traversal and socket creation stay possible while group and other access are
// denied (Rust SOCKET_DIR_MODE).
const socketDirectoryMode = 0o700

// SharedDaemonSocketDirectory returns the fixed executor-local directory that
// every filesystem-restricted sandbox must hide. It deliberately ignores HOME,
// TMPDIR, CODEX_HOME, and command settings (Rust #45984).
func SharedDaemonSocketDirectory() (string, error) {
	temporaryRoot, err := filepath.EvalSymlinks("/tmp")
	if err != nil {
		return "", err
	}
	return filepath.Join(temporaryRoot, fmt.Sprintf("codex-daemon-%d", os.Geteuid())), nil
}

// PrepareSharedDaemonSocketDirectory creates the reserved socket directory and
// rejects symlinks, unsafe owners, and non-0700 modes.
func PrepareSharedDaemonSocketDirectory() (string, error) {
	directory, err := SharedDaemonSocketDirectory()
	if err != nil {
		return "", err
	}
	if err := os.Mkdir(directory, socketDirectoryMode); err != nil && !errors.Is(err, os.ErrExist) {
		return "", err
	}
	info, err := os.Lstat(directory)
	if err != nil {
		return "", err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !info.IsDir() || !ok || int(stat.Uid) != os.Geteuid() || info.Mode().Perm() != socketDirectoryMode {
		return "", errors.New("app-server socket directory must be a user-owned directory with mode 0700")
	}
	return directory, nil
}

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

// connectUnixSocket connects to socketPath, retrying through the resolved
// symlink target when the kernel rejects the advertised path with EINVAL
// (Rust uds::platform::connect_stream).
func connectUnixSocket(ctx context.Context, socketPath string) (net.Conn, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	socketPath = strings.TrimSpace(socketPath)
	var dialer net.Dialer
	conn, err := dialer.DialContext(ctx, "unix", socketPath)
	if err == nil || !errors.Is(err, syscall.EINVAL) {
		return conn, err
	}
	// The advertised path may exceed sun_path even when its symlink target is a
	// short socket path. Resolve it before retrying.
	resolved, resolveErr := filepath.EvalSymlinks(socketPath)
	if resolveErr != nil {
		return nil, resolveErr
	}
	return dialer.DialContext(ctx, "unix", resolved)
}
