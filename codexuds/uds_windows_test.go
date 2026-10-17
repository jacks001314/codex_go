//go:build windows

package codexuds

import (
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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

// TestPreparePrivateSocketDirectoryRejectsInheritedDirectory covers the
// rendezvous hardening contract: an existing directory with broad permissions is
// rejected without attempting a DACL repair.
func TestPreparePrivateSocketDirectoryRejectsInheritedDirectory(t *testing.T) {
	root, err := os.MkdirTemp("", "cxuds")
	if err != nil {
		t.Fatalf("MkdirTemp error = %v", err)
	}
	defer os.RemoveAll(root)
	directory := filepath.Join(root, "app-server-daemon")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatalf("MkdirAll error = %v", err)
	}
	if _, guard, err := ValidatePrivateSocketPath(filepath.Join(directory, "daemon-updater.sock")); err == nil {
		if guard != nil {
			_ = guard.Close()
		}
		t.Fatal("validation accepted a directory without the private contract")
	}
	before, err := windows.GetNamedSecurityInfo(directory, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	if err := PreparePrivateSocketDirectory(directory); err == nil {
		t.Fatal("PreparePrivateSocketDirectory() repaired an existing broad ACL")
	}
	after, err := windows.GetNamedSecurityInfo(directory, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	if before.String() != after.String() {
		t.Fatalf("rejected directory ACL changed: before=%s after=%s", before.String(), after.String())
	}
}

func TestPreparePrivateSocketDirectoryCreatesAndReusesPrivateDirectory(t *testing.T) {
	root, err := os.MkdirTemp("", "cxuds")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(root)
	directory := filepath.Join(root, "private")
	if err := PreparePrivateSocketDirectory(directory); err != nil {
		t.Fatal(err)
	}
	if err := PreparePrivateSocketDirectory(directory); err != nil {
		t.Fatalf("reusing private directory: %v", err)
	}
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

func TestPreparePrivateSocketDirectoryRejectsJunctionsAndVolumeRoots(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target")
	junction := filepath.Join(root, "junction")
	if err := os.MkdirAll(target, 0o700); err != nil {
		t.Fatal(err)
	}
	command := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command",
		"$ErrorActionPreference = 'Stop'; New-Item -ItemType Junction -Path $env:CODEX_TEST_LINK -Target $env:CODEX_TEST_TARGET | Out-Null")
	command.Env = append(os.Environ(), "CODEX_TEST_LINK="+junction, "CODEX_TEST_TARGET="+target)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("create junction: %v: %s", err, strings.TrimSpace(string(output)))
	}
	if err := PreparePrivateSocketDirectory(junction); err == nil {
		t.Fatal("PreparePrivateSocketDirectory() accepted a junction")
	}
	if _, guard, err := ValidatePrivateSocketPath(filepath.Join(junction, "socket")); err == nil {
		if guard != nil {
			_ = guard.Close()
		}
		t.Fatal("ValidatePrivateSocketPath() accepted a junction")
	}

	volumeRoot := `\\?\Volume{00000000-0000-0000-0000-000000000000}\`
	if err := PreparePrivateSocketDirectory(volumeRoot); err == nil {
		t.Fatal("PreparePrivateSocketDirectory() accepted a volume root")
	}
}

func TestPreparePrivateSocketDirectorySupportsExtendedLengthPaths(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, strings.Repeat("a", 150), strings.Repeat("b", 150))
	if err := PreparePrivateSocketDirectory(directory); err != nil {
		t.Fatal(err)
	}
	if err := PreparePrivateSocketDirectory(directory); err != nil {
		t.Fatalf("reusing extended-length private directory: %v", err)
	}
}

func TestValidatePrivateSocketPathPinsDirectoryWithoutCreatingPaths(t *testing.T) {
	root, err := os.MkdirTemp("", "cxpin")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(root)
	directory := filepath.Join(root, "private")
	socketPath := filepath.Join(directory, "socket")
	if _, guard, err := ValidatePrivateSocketPath(socketPath); err == nil {
		if guard != nil {
			_ = guard.Close()
		}
		t.Fatal("validation accepted a missing directory")
	}
	if pathExists(directory) {
		t.Fatal("validation created the missing directory")
	}
	if err := PreparePrivateSocketDirectory(directory); err != nil {
		t.Fatal(err)
	}
	_, guard, err := ValidatePrivateSocketPath(socketPath)
	if err != nil {
		t.Fatalf("validation error = %v", err)
	}
	defer closeGuard(guard)
	if err := os.Rename(directory, filepath.Join(root, "moved")); err == nil {
		t.Fatal("validated socket directory was replaceable while pinned")
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
