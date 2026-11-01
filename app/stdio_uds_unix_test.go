//go:build !windows

package app

import (
	"bufio"
	"bytes"
	"context"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// unixSocketTempDir keeps a bound socket path inside sun_path. macOS reports
// its temporary directory under /var/folders, which overflows the 104-byte
// Darwin limit once a test name and a socket name are appended.
func unixSocketTempDir(t *testing.T) string {
	t.Helper()
	base := ""
	if runtime.GOOS == "darwin" {
		base = "/tmp"
	}
	dir, err := os.MkdirTemp(base, "uds")
	if err != nil {
		t.Fatalf("MkdirTemp error = %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

func TestStdioToUDSBridgesUnixSocket(t *testing.T) {
	socketPath := filepath.Join(unixSocketTempDir(t), "codex.sock")
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("Listen unix error = %v", err)
	}
	defer listener.Close()
	done := make(chan error, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			done <- err
			return
		}
		defer conn.Close()
		line, err := bufio.NewReader(conn).ReadString('\n')
		if err != nil {
			done <- err
			return
		}
		_, err = conn.Write([]byte("reply:" + line))
		done <- err
	}()

	var stdout bytes.Buffer
	if err := Run(context.Background(), []string{"stdio-to-uds", socketPath}, strings.NewReader("hello\n"), &stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("stdio-to-uds returned error: %v", err)
	}
	if stdout.String() != "reply:hello\n" {
		t.Fatalf("stdout = %q", stdout.String())
	}
	if err := <-done; err != nil {
		t.Fatalf("server error = %v", err)
	}
}

// TestStdioToUDSBridgesLongSymlinkSocket mirrors the Rust consumer of #48772:
// codex-rs/stdio-to-uds connects through codex_uds::UnixStream::connect, so a
// long advertised alias must still reach the short socket it points at.
func TestStdioToUDSBridgesLongSymlinkSocket(t *testing.T) {
	physicalPath := filepath.Join(unixSocketTempDir(t), "codex.sock")
	listener, err := net.Listen("unix", physicalPath)
	if err != nil {
		t.Fatalf("Listen unix error = %v", err)
	}
	defer listener.Close()
	done := make(chan error, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			done <- err
			return
		}
		defer conn.Close()
		line, err := bufio.NewReader(conn).ReadString('\n')
		if err != nil {
			done <- err
			return
		}
		_, err = conn.Write([]byte("reply:" + line))
		done <- err
	}()

	advertisedPath := filepath.Join(unixSocketTempDir(t), strings.Repeat("y", 120), "codex.sock")
	if err := os.MkdirAll(filepath.Dir(advertisedPath), 0o700); err != nil {
		t.Fatalf("mkdir advertised socket dir: %v", err)
	}
	if err := os.Symlink(physicalPath, advertisedPath); err != nil {
		t.Fatalf("symlink advertised socket: %v", err)
	}
	if len(advertisedPath) <= 108 {
		t.Fatalf("advertised path %q (%d bytes) does not exceed sun_path", advertisedPath, len(advertisedPath))
	}

	var stdout bytes.Buffer
	if err := Run(context.Background(), []string{"stdio-to-uds", advertisedPath}, strings.NewReader("hello\n"), &stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("stdio-to-uds through a long symlink path returned error: %v", err)
	}
	if stdout.String() != "reply:hello\n" {
		t.Fatalf("stdout = %q", stdout.String())
	}
	if err := <-done; err != nil {
		t.Fatalf("server error = %v", err)
	}
}
