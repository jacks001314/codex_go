//go:build !windows

package appserver

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"codex_go/codexuds"
)

func serveUnixSocket(ctx context.Context, socketPath string, routerFactory func() *RuntimeRouter, access DaemonShutdownAccess, shutdown func(), compressionEnabled bool) error {
	socketPath = strings.TrimSpace(socketPath)
	if socketPath == "" {
		return fmt.Errorf("%w: socket path is empty", ErrInvalidRequest)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	rendezvousPath, err := filepath.Abs(socketPath)
	if err != nil {
		return err
	}
	if err := prepareUnixSocketRendezvousParent(rendezvousPath); err != nil {
		return err
	}
	physicalPath, err := protectedUnixSocketPath(rendezvousPath)
	if err != nil {
		return err
	}
	if _, err := codexuds.PrepareSharedDaemonSocketDirectory(); err != nil {
		return err
	}
	if err := prepareProtectedUnixSocketPath(physicalPath); err != nil {
		return err
	}
	if err := prepareUnixSocketRendezvous(rendezvousPath, physicalPath); err != nil {
		return err
	}
	listener, err := net.Listen("unix", physicalPath)
	if err != nil {
		return err
	}
	defer listener.Close()
	if err := os.Symlink(physicalPath, rendezvousPath); err != nil {
		return err
	}
	defer func() {
		// Preserve a replacement published at the advertised path.
		if target, err := os.Readlink(rendezvousPath); err == nil && target == physicalPath {
			_ = os.Remove(rendezvousPath)
		}
		_ = os.Remove(physicalPath)
	}()
	return serveUnixSocketHTTP(ctx, listener, routerFactory, access, shutdown, compressionEnabled)
}

// prepareUnixSocketRendezvousParent preserves an existing parent and rejects
// parents another user could replace socket entries in (Rust #45984).
func prepareUnixSocketRendezvousParent(rendezvousPath string) error {
	parent := filepath.Dir(rendezvousPath)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return err
	}
	info, err := os.Lstat(parent)
	if err != nil {
		return err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !info.IsDir() || !ok {
		return fmt.Errorf("socket parent must be a directory: %s", parent)
	}
	if int(stat.Uid) != 0 && int(stat.Uid) != os.Geteuid() {
		return fmt.Errorf("socket parent must be owned by the user or root: %s", parent)
	}
	if info.Mode().Perm()&0o022 != 0 && info.Mode()&os.ModeSticky == 0 {
		return fmt.Errorf("socket parent must prevent other users from replacing socket entries: %s", parent)
	}
	return nil
}

// protectedUnixSocketPath derives the physical socket path from the advertised
// rendezvous path. The hash keeps the socket in a fixed user-owned directory
// while remaining deterministic across restarts.
func protectedUnixSocketPath(rendezvousPath string) (string, error) {
	parent, err := filepath.EvalSymlinks(filepath.Dir(rendezvousPath))
	if err != nil {
		return "", err
	}
	name := filepath.Base(rendezvousPath)
	if name == "." || name == string(filepath.Separator) {
		return "", fmt.Errorf("socket path must have a filename")
	}
	directory, err := codexuds.SharedDaemonSocketDirectory()
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(filepath.Join(parent, name)))
	return filepath.Join(directory, hex.EncodeToString(sum[:])), nil
}

func prepareProtectedUnixSocketPath(physicalPath string) error {
	if err := os.Remove(physicalPath); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// prepareUnixSocketRendezvous removes only our deterministic alias or a stale
// socket left by a crashed daemon; arbitrary paths are rejected.
func prepareUnixSocketRendezvous(rendezvousPath, physicalPath string) error {
	info, err := os.Lstat(rendezvousPath)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		target, readErr := os.Readlink(rendezvousPath)
		if readErr != nil {
			return readErr
		}
		if target != physicalPath {
			return fmt.Errorf("socket path already exists and is not the protected rendezvous: %s", rendezvousPath)
		}
		return os.Remove(rendezvousPath)
	}
	if info.Mode()&os.ModeSocket == 0 {
		return fmt.Errorf("socket path already exists and is not a socket: %s", rendezvousPath)
	}
	dialer := net.Dialer{Timeout: 200 * time.Millisecond}
	conn, dialErr := dialer.Dial("unix", rendezvousPath)
	if dialErr == nil {
		_ = conn.Close()
		return fmt.Errorf("socket path is already in use: %s", rendezvousPath)
	}
	return os.Remove(rendezvousPath)
}

// verifiedLocalDaemonSocket applies Rust #54421's Unix directory contract to the
// directory that holds the advertised socket and to the rendezvous alias. The
// advertised path must be our deterministic symlink to the protected physical
// socket, so an SSH/socat forward bound directly at the advertised path is
// rejected.
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
	protected, err := protectedUnixSocketPath(absolute)
	if err != nil {
		return "", false
	}
	target, err := os.Readlink(absolute)
	if err != nil || target != protected {
		return "", false
	}
	shared, err := codexuds.SharedDaemonSocketDirectory()
	if err != nil {
		return "", false
	}
	sharedInfo, err := os.Lstat(shared)
	if err != nil || !sharedInfo.IsDir() {
		return "", false
	}
	sharedStat, ok := sharedInfo.Sys().(*syscall.Stat_t)
	if !ok || int(sharedStat.Uid) != os.Geteuid() || sharedInfo.Mode().Perm() != 0o700 {
		return "", false
	}
	return absolute, true
}
