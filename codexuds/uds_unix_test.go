//go:build !windows

package codexuds

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestConnectUnixSocketThroughLongSymlinkPaths mirrors Rust #48772
// (app-server-transport/src/transport/unix_socket_tests.rs
// `long_control_socket_paths_connect_to_distinct_daemons`): an advertised
// control socket path can exceed sun_path even when its symlink target is a
// short socket path, so a client must resolve the advertised alias before
// connecting.
func TestConnectUnixSocketThroughLongSymlinkPaths(t *testing.T) {
	physicalDir := t.TempDir()
	longParent := filepath.Join(t.TempDir(), strings.Repeat("x", 120))

	advertised := make([]string, 0, 2)
	targets := make([]string, 0, 2)
	for _, name := range []string{"first", "second"} {
		codexHome := filepath.Join(longParent, name)
		advertisedPath := filepath.Join(codexHome, "app-server-control", "app-server-control.sock")
		if err := os.MkdirAll(filepath.Dir(advertisedPath), 0o700); err != nil {
			t.Fatalf("mkdir advertised control socket dir: %v", err)
		}
		physicalPath := filepath.Join(physicalDir, name+".sock")
		listener, err := net.Listen("unix", physicalPath)
		if err != nil {
			t.Fatalf("listen %s: %v", physicalPath, err)
		}
		t.Cleanup(func() { _ = listener.Close() })
		go func(listener net.Listener) {
			for {
				conn, err := listener.Accept()
				if err != nil {
					return
				}
				_ = conn.Close()
			}
		}(listener)
		if err := os.Symlink(physicalPath, advertisedPath); err != nil {
			t.Fatalf("symlink advertised control socket: %v", err)
		}
		// The advertised alias only exercises the kernel's sun_path limit when it
		// is longer than every platform's 104/108-byte sockaddr field.
		if len(advertisedPath) <= 108 {
			t.Fatalf("advertised path %q (%d bytes) does not exceed sun_path", advertisedPath, len(advertisedPath))
		}
		target, err := os.Readlink(advertisedPath)
		if err != nil {
			t.Fatalf("read advertised socket target: %v", err)
		}
		advertised = append(advertised, advertisedPath)
		targets = append(targets, target)
	}
	if targets[0] == targets[1] {
		t.Fatalf("advertised sockets share one symlink target: %q", targets[0])
	}

	for _, socketPath := range advertised {
		if conn, err := net.Dial("unix", socketPath); err == nil {
			_ = conn.Close()
			t.Fatalf("raw dial of the over-long advertised path %q unexpectedly succeeded", socketPath)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		conn, err := ConnectUnixSocket(ctx, socketPath)
		cancel()
		if err != nil {
			t.Fatalf("ConnectUnixSocket(%q) error = %v", socketPath, err)
		}
		_ = conn.Close()
	}
}
