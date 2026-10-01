//go:build !windows

package appserverdaemon

import "net"

// prepareSocketDialPath is the identity hook off Windows, where the control
// socket directory is protected by its 0700 mode.
func prepareSocketDialPath(socketPath string) (string, func(), error) {
	return socketPath, nil, nil
}

// ensureSocketPeerAllowed is a no-op off Windows, where a same-host AF_UNIX
// peer is identified by the file system.
func ensureSocketPeerAllowed(net.Conn) error {
	return nil
}
