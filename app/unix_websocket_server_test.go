package app

import (
	"context"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/coder/websocket"
)

// startUnixWebSocketTestServer serves handler over a WebSocket-accepting unix
// socket, which is the app-server control socket's real transport
// (Rust app-server-transport::start_control_socket_acceptor).
func startUnixWebSocketTestServer(t *testing.T, handler func(ctx context.Context, conn *websocket.Conn)) string {
	t.Helper()
	directory, err := os.MkdirTemp("", "wsuds")
	if err != nil {
		t.Fatalf("MkdirTemp error = %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(directory) })
	path := filepath.Join(directory, "control.sock")
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatalf("listen error = %v", err)
	}
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{})
		if err != nil {
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "")
		handler(r.Context(), conn)
	})}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() {
		_ = server.Close()
		_ = listener.Close()
	})
	return path
}
