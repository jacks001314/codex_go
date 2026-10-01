//go:build windows

package codexuds

import (
	"net"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
)

func netListen(path string) (net.Listener, error) {
	return net.Listen("unix", path)
}

// unixPair accepts one connection on listener and returns both ends.
func unixPair(t *testing.T, listener net.Listener, path string) (net.Conn, net.Conn) {
	t.Helper()
	accepted := make(chan net.Conn, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			accepted <- nil
			return
		}
		accepted <- conn
	}()
	client, err := net.Dial("unix", path)
	if err != nil {
		t.Fatalf("dial error = %v", err)
	}
	server := <-accepted
	if server == nil {
		t.Fatal("listener did not accept the connection")
	}
	return server, client
}

// TestPreparePrivateSocketDirectorySecuresAnInheritedDirectory covers the
// upgrade path: a state directory an earlier build created with the default
// DACL is secured in place instead of breaking the installation, and a directory
// that does not meet the contract is still rejected for validation.
func TestPreparePrivateSocketDirectorySecuresAnInheritedDirectory(t *testing.T) {
	root, err := os.MkdirTemp("", "cxuds")
	if err != nil {
		t.Fatalf("MkdirTemp error = %v", err)
	}
	defer os.RemoveAll(root)
	directory := filepath.Join(root, "app-server-daemon")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatalf("MkdirAll error = %v", err)
	}
	// Until the contract is applied, validation refuses the directory.
	if _, guard, err := ValidatePrivateSocketPath(filepath.Join(directory, "daemon-updater.sock")); err == nil {
		if guard != nil {
			_ = guard.Close()
		}
		t.Fatal("validation accepted a directory without the private contract")
	}
	if err := PreparePrivateSocketDirectory(directory); err != nil {
		t.Fatalf("PreparePrivateSocketDirectory(inherited) error = %v", err)
	}
	if _, guard, err := ValidatePrivateSocketPath(filepath.Join(directory, "daemon-updater.sock")); err != nil {
		t.Fatalf("validation after securing error = %v", err)
	} else if guard != nil {
		_ = guard.Close()
	}
	// A socket the secured directory owns can now be bound and dialed.
	path := filepath.Join(directory, "daemon-updater.sock")
	listener, err := netListen(path)
	if err != nil {
		t.Fatalf("listen error = %v", err)
	}
	defer listener.Close()
	server, client := unixPair(t, listener, path)
	defer server.Close()
	defer client.Close()
	if _, err := client.Write([]byte("update\n")); err != nil {
		t.Fatalf("write error = %v", err)
	}
}

// TestEnsureNonElevatedPeerNamesThePeerLikeRust pins the SIO_AF_UNIX_GETPEERPID
// control code: a socket pair this runtime created authenticates as the
// current, non-elevated user. A wrong control code makes Windows answer
// WSAEOPNOTSUPP, which surfaces as ErrPeerIdentityUnavailable.
func TestEnsureNonElevatedPeerNamesThePeerLikeRust(t *testing.T) {
	if windows.GetCurrentProcessToken().IsElevated() {
		t.Skip("the peer policy refuses an elevated process")
	}
	root, err := os.MkdirTemp("", "cxpeer")
	if err != nil {
		t.Fatalf("MkdirTemp error = %v", err)
	}
	defer os.RemoveAll(root)
	path := filepath.Join(root, "peer.sock")
	listener, err := netListen(path)
	if err != nil {
		t.Fatalf("listen error = %v", err)
	}
	defer listener.Close()
	server, client := unixPair(t, listener, path)
	defer server.Close()
	defer client.Close()
	if err := EnsureNonElevatedPeer(client); err != nil {
		t.Fatalf("EnsureNonElevatedPeer(client) error = %v", err)
	}
	if err := EnsureNonElevatedPeer(server); err != nil {
		t.Fatalf("EnsureNonElevatedPeer(server) error = %v", err)
	}
}
