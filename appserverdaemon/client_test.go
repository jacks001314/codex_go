package appserverdaemon

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/coder/websocket"

	"codex_go/remotecontrol"
)

// startUnixWebSocketControlServer serves handler over a WebSocket-accepting unix
// socket, which is the control socket's real transport (Rust
// app-server-transport::start_control_socket_acceptor).
func startUnixWebSocketControlServer(t *testing.T, handler func(ctx context.Context, conn *websocket.Conn)) string {
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

// dialControlClient upgrades a test connection the way the real client does.
func dialControlClient(t *testing.T, socketPath string) *localSocketRemoteControlClient {
	t.Helper()
	conn, err := dialUnixSocketWebSocket(socketPath, 5*time.Second)
	if err != nil {
		t.Fatalf("dialUnixSocketWebSocket error = %v", err)
	}
	t.Cleanup(func() { _ = conn.Close(websocket.StatusNormalClosure, "") })
	return newLocalSocketRemoteControlClient(conn)
}

// serveControlExchange reads one text frame, requires its method, and answers
// with result.
func serveControlExchange(t *testing.T, ctx context.Context, conn *websocket.Conn, wantMethod string, result any) {
	t.Helper()
	messageType, data, err := conn.Read(ctx)
	if err != nil {
		t.Errorf("server read error = %v", err)
		return
	}
	if messageType != websocket.MessageText {
		t.Errorf("server read message type = %v, want text", messageType)
		return
	}
	var request JSONRPCRequest
	if err := json.Unmarshal(data, &request); err != nil {
		t.Errorf("server decode error = %v", err)
		return
	}
	if request.Method != wantMethod {
		t.Errorf("server method = %q, want %q", request.Method, wantMethod)
		return
	}
	payload, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      request.ID,
		"result":  result,
	})
	if err != nil {
		t.Errorf("server encode error = %v", err)
		return
	}
	if err := conn.Write(ctx, websocket.MessageText, payload); err != nil {
		t.Errorf("server write error = %v", err)
	}
}

func TestLocalSocketRemoteControlClientEnable(t *testing.T) {
	socketPath := startUnixWebSocketControlServer(t, func(ctx context.Context, conn *websocket.Conn) {
		serveControlExchange(t, ctx, conn, "initialize", map[string]any{"userAgent": "codex/0.0.0"})
		serveControlExchange(t, ctx, conn, "initialized", map[string]any{})
		serveControlExchange(t, ctx, conn, "remoteControl/enable", map[string]any{
			"status":         remotecontrol.StatusConnected,
			"serverName":     "test-machine",
			"installationId": "install-1",
			"environmentId":  "env-1",
		})
	})

	status, err := dialControlClient(t, socketPath).enableRemoteControl()
	if err != nil {
		t.Fatalf("enableRemoteControl returned error: %v", err)
	}
	if status.Status != remotecontrol.StatusConnected || status.ServerName != "test-machine" || status.EnvironmentID == nil || *status.EnvironmentID != "env-1" {
		t.Fatalf("status = %#v", status)
	}
}

func TestLocalSocketRemoteControlClientDisable(t *testing.T) {
	socketPath := startUnixWebSocketControlServer(t, func(ctx context.Context, conn *websocket.Conn) {
		serveControlExchange(t, ctx, conn, "initialize", map[string]any{"userAgent": "codex/0.0.0"})
		serveControlExchange(t, ctx, conn, "initialized", map[string]any{})
		serveControlExchange(t, ctx, conn, "remoteControl/disable", map[string]any{
			"status":         remotecontrol.StatusDisabled,
			"serverName":     "test-machine",
			"installationId": "install-1",
			"environmentId":  "env-1",
		})
	})

	status, err := dialControlClient(t, socketPath).disableRemoteControl()
	if err != nil {
		t.Fatalf("disableRemoteControl returned error: %v", err)
	}
	if status.Status != remotecontrol.StatusDisabled || status.ServerName != "test-machine" || status.EnvironmentID == nil || *status.EnvironmentID != "env-1" {
		t.Fatalf("status = %#v", status)
	}
}
