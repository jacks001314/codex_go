//go:build !windows

package appserver

import (
	"bufio"
	"context"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestServeUnixSocketHandlesJSONRPCLine(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	socketPath := filepath.Join(t.TempDir(), "codex.sock")
	done := make(chan error, 1)
	go func() {
		done <- ServeUnixSocket(ctx, &UnixSocketOptions{
			CodexHome: t.TempDir(),
			Listen:    "unix://" + socketPath,
		})
	}()

	conn := dialUnixSocketForTest(t, socketPath)
	defer conn.Close()
	_, err := conn.Write([]byte(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"clientInfo":{"name":"test","version":"1"}}}` + "\n"))
	if err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	line, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil {
		t.Fatalf("ReadString() error = %v", err)
	}
	if !strings.Contains(line, `"id":1`) || !strings.Contains(line, `"result"`) {
		t.Fatalf("response line = %q", line)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("ServeUnixSocket returned error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for ServeUnixSocket")
	}
}

func dialUnixSocketForTest(t *testing.T, socketPath string) net.Conn {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for {
		conn, err := net.Dial("unix", socketPath)
		if err == nil {
			return conn
		}
		if time.Now().After(deadline) {
			t.Fatalf("Dial unix %s error = %v", socketPath, err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
