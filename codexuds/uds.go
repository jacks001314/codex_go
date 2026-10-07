// Package codexuds holds the cross-platform Unix-domain-socket helpers the
// shared app-server relies on (Rust codex-rs/uds).
//
// On Windows the rendezvous directory is protected with a user-only DACL and a
// client requires an implicit peer to be the same, non-elevated user before it
// sends data. On Unix those guarantees come from the socket directory's 0700
// mode and the file-system identity of the peer, which appserver applies
// directly, so every helper here is a no-op there.
package codexuds

import (
	"context"
	"errors"
	"net"
	"os"
)

var (
	// ErrPeerIdentityUnavailable reports that the platform would not name the
	// socket peer's process: SIO_AF_UNIX_GETPEERPID failed or reported no PID.
	// Callers refuse the connection, exactly as Rust propagates the error
	// (a correct SIO_AF_UNIX_GETPEERPID control code reports the peer).
	ErrPeerIdentityUnavailable = errors.New("socket peer identity is unavailable")
	// ErrPeerNotAllowed reports a peer from another user or an elevated token.
	ErrPeerNotAllowed = errors.New("implicit daemon connection requires non-elevated current-user tokens")
)

// CheckSocketPath rejects a socket path the platform cannot address. Windows
// encodes the path into the 108-byte AF_UNIX sun_path field, so a longer path
// can never be bound or dialed (Rust uds_windows::sockaddr_un).
func CheckSocketPath(socketPath string) error {
	return checkSocketPath(socketPath)
}

// PreparePrivateSocketDirectory creates socketDir with the platform's
// user-only protection, or accepts an existing directory that already meets
// that contract (Rust uds::prepare_private_socket_directory).
func PreparePrivateSocketDirectory(socketDir string) error {
	return preparePrivateSocketDirectory(socketDir)
}

// ValidatePrivateSocketPath validates the directory that holds socketPath
// without creating or changing it, and returns the validated path plus a guard
// that pins the directory for as long as the caller keeps it open
// (Rust uds::validate_private_socket_path).
func ValidatePrivateSocketPath(socketPath string) (string, *os.File, error) {
	return validatePrivateSocketPath(socketPath)
}

// PrepareControlSocketPath makes socketPath bindable: an existing socket that
// still answers is refused, and only a genuinely stale path is removed
// (Rust app-server-transport::prepare_control_socket_path).
func PrepareControlSocketPath(socketPath string) error {
	return prepareControlSocketPath(socketPath)
}

// EnsureNonElevatedPeer requires the socket peer to belong to the current user
// with neither process elevated. Call it before sending application data
// (Rust uds::UnixStream::ensure_non_elevated_peer).
func EnsureNonElevatedPeer(conn net.Conn) error {
	return ensureNonElevatedPeer(conn)
}

// ConnectUnixSocket connects to an advertised Unix socket path. On Unix an
// advertised path can exceed the kernel's sun_path limit even when its symlink
// target is short enough to connect to, so the path is resolved and the
// connection retried (Rust uds::UnixStream::connect -> platform::connect_stream).
func ConnectUnixSocket(ctx context.Context, socketPath string) (net.Conn, error) {
	return connectUnixSocket(ctx, socketPath)
}
