//go:build !windows

package appserver

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func TestServeUnixSocketServesJSONRPCOverWebSocket(t *testing.T) {
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

	conn := dialControlSocketWebSocket(t, socketPath)
	writeCtx, cancelWrite := context.WithTimeout(context.Background(), 15*time.Second)
	err := conn.Write(writeCtx, websocket.MessageText, []byte(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"clientInfo":{"name":"test","version":"1"}}}`))
	cancelWrite()
	if err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	readCtx, cancelRead := context.WithTimeout(context.Background(), 15*time.Second)
	_, data, err := conn.Read(readCtx)
	cancelRead()
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if !strings.Contains(string(data), `"id":1`) || !strings.Contains(string(data), `"result"`) {
		t.Fatalf("response = %q", string(data))
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
