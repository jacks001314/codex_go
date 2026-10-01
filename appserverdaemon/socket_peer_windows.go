//go:build windows

package appserverdaemon

import (
	"net"

	"codex_go/codexuds"
)

// prepareSocketDialPath validates the private control-socket directory before a
// connection is attempted, and returns a release function for the guard that
// pins the validated directory (Rust
// codex_uds::validate_private_socket_path).
func prepareSocketDialPath(socketPath string) (string, func(), error) {
	path, guard, err := codexuds.ValidatePrivateSocketPath(socketPath)
	if err != nil {
		return "", nil, err
	}
	return path, func() {
		if guard != nil {
			_ = guard.Close()
		}
	}, nil
}

// ensureSocketPeerAllowed requires an implicit daemon peer to be the current,
// non-elevated user (Rust SocketPeerPolicy::NonElevatedCurrentUser). The kernel
// names the peer's process through SIO_AF_UNIX_GETPEERPID, and its token must
// match this process's; a peer from another user, an elevated token, or an
// identity the kernel refuses to name is rejected (Rust propagates the error).
func ensureSocketPeerAllowed(conn net.Conn) error {
	return codexuds.EnsureNonElevatedPeer(conn)
}
