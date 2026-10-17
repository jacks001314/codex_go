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

func TestServeUnixSocketNegotiatesZstdCompression(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	socketPath := filepath.Join(t.TempDir(), "codex.sock")
	done := make(chan error, 1)
	compressionEnabled := true
	go func() {
		done <- ServeUnixSocket(ctx, &UnixSocketOptions{
			CodexHome:          t.TempDir(),
			Listen:             "unix://" + socketPath,
			CompressionEnabled: &compressionEnabled,
		})
	}()

	conn, response := dialControlSocketWebSocketWithHeader(t, socketPath, map[string][]string{
		WebsocketCompressionHeader: {WebsocketZstdStreamProtocol},
	})
	if got := response.Header.Get(WebsocketCompressionHeader); got != WebsocketZstdStreamProtocol {
		t.Fatalf("compression response header = %q", got)
	}
	encoder, err := NewZstdEncoder()
	if err != nil {
		t.Fatalf("NewZstdEncoder() error = %v", err)
	}
	defer encoder.Close()
	decoder, err := NewZstdDecoder(MaxWebsocketDecompressedMessageSize)
	if err != nil {
		t.Fatalf("NewZstdDecoder() error = %v", err)
	}
	defer decoder.Close()

	writeCtx, cancelWrite := context.WithTimeout(context.Background(), 15*time.Second)
	payload, err := encoder.Encode(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"clientInfo":{"name":"test","version":"1"}}}`)
	if err == nil {
		err = conn.Write(writeCtx, websocket.MessageBinary, payload)
	}
	cancelWrite()
	if err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	readCtx, cancelRead := context.WithTimeout(context.Background(), 15*time.Second)
	messageType, data, err := conn.Read(readCtx)
	cancelRead()
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if messageType != websocket.MessageBinary {
		t.Fatalf("message type = %v", messageType)
	}
	decoded, err := decoder.Decode(data)
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if !strings.Contains(decoded, `"id":1`) || !strings.Contains(decoded, `"result"`) {
		t.Fatalf("response = %q", decoded)
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

func TestServeUnixSocketCompressionFeatureGate(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	socketPath := filepath.Join(t.TempDir(), "codex.sock")
	done := make(chan error, 1)
	compressionEnabled := false
	go func() {
		done <- ServeUnixSocket(ctx, &UnixSocketOptions{
			CodexHome:          t.TempDir(),
			Listen:             "unix://" + socketPath,
			CompressionEnabled: &compressionEnabled,
		})
	}()

	conn, response := dialControlSocketWebSocketWithHeader(t, socketPath, map[string][]string{
		WebsocketCompressionHeader: {WebsocketZstdStreamProtocol},
	})
	if got := response.Header.Get(WebsocketCompressionHeader); got != "" {
		t.Fatalf("disabled compression feature was acknowledged with %q", got)
	}
	writeCtx, cancelWrite := context.WithTimeout(context.Background(), 15*time.Second)
	err := conn.Write(writeCtx, websocket.MessageText, []byte(
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"clientInfo":{"name":"test","version":"1"}}}`,
	))
	cancelWrite()
	if err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	readCtx, cancelRead := context.WithTimeout(context.Background(), 15*time.Second)
	messageType, data, err := conn.Read(readCtx)
	cancelRead()
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if messageType != websocket.MessageText || !strings.Contains(string(data), `"id":1`) {
		t.Fatalf("fallback response type=%v data=%q", messageType, data)
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
