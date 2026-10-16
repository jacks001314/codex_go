//go:build !windows

package appserver

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

func serveUnixSocket(ctx context.Context, socketPath string, routerFactory func() *RuntimeRouter, access DaemonShutdownAccess, shutdown func()) error {
	socketPath = strings.TrimSpace(socketPath)
	if socketPath == "" {
		return fmt.Errorf("%w: socket path is empty", ErrInvalidRequest)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ensureUnixSocketParent(socketPath); err != nil {
		return err
	}
	_ = os.Remove(socketPath)
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		return err
	}
	defer listener.Close()
	defer os.Remove(socketPath)
	return serveUnixSocketHTTP(ctx, listener, routerFactory, access, shutdown)
}

// verifiedLocalDaemonSocket applies Rust #54421's Unix directory contract to the
// directory that holds the advertised socket: it must be a directory owned by the
// current effective user with exactly 0700 permissions, which is the same
// bind-time contract codexuds::prepare_private_socket_directory applies here.
//
// Rust additionally requires the advertised rendezvous path to be a symlink whose
// target is the sha256-derived protected socket path (readlink ==
// protected_socket_path), which is what rejects an SSH/socat forward bound
// directly at the advertised path. This daemon binds the advertised control
// socket directly and Go has no protected-path derivation (no protectedSocketPath
// or rendezvous helpers exist outside tests), so that half has no carrier on this
// platform; the directory contract above is the strongest equivalent available.
func verifiedLocalDaemonSocket(socketPath string) (string, bool) {
	socketPath = strings.TrimSpace(socketPath)
	if socketPath == "" {
		return "", false
	}
	absolute, err := filepath.Abs(socketPath)
	if err != nil {
		return "", false
	}
	directory, err := os.Stat(filepath.Dir(absolute))
	if err != nil || !directory.IsDir() {
		return "", false
	}
	stat, ok := directory.Sys().(*syscall.Stat_t)
	if !ok || int(stat.Uid) != os.Geteuid() {
		return "", false
	}
	if directory.Mode().Perm() != 0o700 {
		return "", false
	}
	return absolute, true
}
