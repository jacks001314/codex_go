//go:build !windows

package app

import (
	"bufio"
	"bytes"
	"context"
	"net"
	"path/filepath"
	"strings"
	"testing"
)

func TestStdioToUDSBridgesUnixSocket(t *testing.T) {
	socketPath := filepath.Join(t.TempDir(), "codex.sock")
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
