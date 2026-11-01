package appserverdaemon

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"codex_go/codexuds"
	"codex_go/remotecontrol"
)

// startPrivateUnixWebSocketControlServer serves handler over a control socket
// whose directory satisfies the daemon's private-directory contract, which
// StartRemoteControlPairingOnSocket validates before connecting.
func startPrivateUnixWebSocketControlServer(t *testing.T, handler func(ctx context.Context, conn *websocket.Conn)) string {
	t.Helper()
	base, err := os.MkdirTemp("", "wsuds")
	if err != nil {
		t.Fatalf("MkdirTemp error = %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(base) })
	directory := filepath.Join(base, "private")
	if err := codexuds.PreparePrivateSocketDirectory(directory); err != nil {
		t.Fatalf("PreparePrivateSocketDirectory error = %v", err)
	}
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

// readControlRequest reads one control-socket text frame and decodes it.
func readControlRequest(t *testing.T, ctx context.Context, conn *websocket.Conn) JSONRPCRequest {
	t.Helper()
	messageType, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("server read error = %v", err)
	}
	if messageType != websocket.MessageText {
		t.Fatalf("server message type = %v, want text", messageType)
	}
	var request JSONRPCRequest
	if err := json.Unmarshal(data, &request); err != nil {
		t.Fatalf("server decode error = %v", err)
	}
	return request
}

// writeControlResult answers request with result.
func writeControlResult(t *testing.T, ctx context.Context, conn *websocket.Conn, request JSONRPCRequest, result any) {
	t.Helper()
	payload, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": result})
	if err != nil {
		t.Fatalf("server encode error = %v", err)
	}
	if err := conn.Write(ctx, websocket.MessageText, payload); err != nil {
		t.Fatalf("server write error = %v", err)
	}
}

// TestStartRemoteControlPairingRequestsManualCodeLikeRust covers Rust #57228's
// daemon pairing client: connect, initialize, then send
// remoteControl/pairing/start with manual_code=true and return the manual code.
func TestStartRemoteControlPairingRequestsManualCodeLikeRust(t *testing.T) {
	requested := make(chan remotecontrol.PairingStartParams, 1)
	socketPath := startPrivateUnixWebSocketControlServer(t, func(ctx context.Context, conn *websocket.Conn) {
		serveControlExchange(t, ctx, conn, "initialize", map[string]any{"userAgent": "codex/0.0.0"})
		serveControlExchange(t, ctx, conn, "initialized", map[string]any{})
		request := readControlRequest(t, ctx, conn)
		if request.Method != "remoteControl/pairing/start" {
			t.Errorf("server method = %q, want remoteControl/pairing/start", request.Method)
			return
		}
		var params remotecontrol.PairingStartParams
		if err := json.Unmarshal(mustMarshal(t, request.Params), &params); err != nil {
			t.Errorf("server params decode error = %v", err)
			return
		}
		requested <- params
		manual := "ABCD-EFGH"
		writeControlResult(t, ctx, conn, request, map[string]any{
			"pairingCode":       "pairing-code",
			"manualPairingCode": manual,
			"environmentId":     "env-1",
			"expiresAt":         1234,
		})
	})

	response, err := StartRemoteControlPairingOnSocket(socketPath)
	if err != nil {
		t.Fatalf("StartRemoteControlPairingOnSocket() error = %v", err)
	}
	if response.ManualPairingCode == nil || *response.ManualPairingCode != "ABCD-EFGH" {
		t.Fatalf("manual pairing code = %v, want ABCD-EFGH", response.ManualPairingCode)
	}
	if response.PairingCode != "pairing-code" || response.EnvironmentID != "env-1" {
		t.Fatalf("pairing response = %#v", response)
	}
	select {
	case params := <-requested:
		if !params.ManualCode {
			t.Fatal("pairing request did not ask for a manual code")
		}
	case <-time.After(time.Second):
		t.Fatal("server did not observe the pairing request")
	}
}

// TestStartRemoteControlPairingDeadlineSurvivesStatusNotificationLikeRust
// covers Rust #57228: pairing reads its response without a per-message timeout,
// so a status notification followed by a slow answer does not trip the
// two-second control-socket response timeout.
func TestStartRemoteControlPairingDeadlineSurvivesStatusNotificationLikeRust(t *testing.T) {
	socketPath := startPrivateUnixWebSocketControlServer(t, func(ctx context.Context, conn *websocket.Conn) {
		serveControlExchange(t, ctx, conn, "initialize", map[string]any{"userAgent": "codex/0.0.0"})
		serveControlExchange(t, ctx, conn, "initialized", map[string]any{})
		request := readControlRequest(t, ctx, conn)
		notification, err := json.Marshal(map[string]any{
			"jsonrpc": "2.0",
			"method":  "remoteControl/status/changed",
			"params": map[string]any{
				"status":         remotecontrol.StatusConnecting,
				"serverName":     "test-machine",
				"installationId": "install-1",
			},
		})
		if err != nil {
			t.Errorf("encode notification: %v", err)
			return
		}
		if err := conn.Write(ctx, websocket.MessageText, notification); err != nil {
			t.Errorf("write notification: %v", err)
			return
		}
		// Longer than ControlSocketResponseTimeout: a per-message timeout would
		// fail here even though the overall 35-second pairing budget remains.
		time.Sleep(ControlSocketResponseTimeout + 500*time.Millisecond)
		manual := "WXYZ-1234"
		writeControlResult(t, ctx, conn, request, map[string]any{
			"pairingCode":       "pairing-code",
			"manualPairingCode": manual,
			"environmentId":     "env-1",
		})
	})

	started := time.Now()
	response, err := StartRemoteControlPairingOnSocket(socketPath)
	if err != nil {
		t.Fatalf("pairing after a status notification returned error: %v", err)
	}
	if response.ManualPairingCode == nil || *response.ManualPairingCode != "WXYZ-1234" {
		t.Fatalf("manual pairing code = %v, want WXYZ-1234", response.ManualPairingCode)
	}
	if elapsed := time.Since(started); elapsed < ControlSocketResponseTimeout {
		t.Fatalf("pairing returned after %s; the delayed response was not awaited", elapsed)
	}
}

// delayedFirstAcceptListener delays the first Accept so the client's WebSocket
// handshake waits behind a busy server.
type delayedFirstAcceptListener struct {
	net.Listener
	delay time.Duration
	once  sync.Once
}

func (l *delayedFirstAcceptListener) Accept() (net.Conn, error) {
	l.once.Do(func() { time.Sleep(l.delay) })
	return l.Listener.Accept()
}

// TestStartRemoteControlPairingToleratesSlowHandshakeLikeRust pins the connect
// poll: the retry delay paces the loop and must not bound the WebSocket
// handshake. With a short per-attempt handshake timeout the client abandons the
// connection while the server is still accepting, and the server reads EOF (a
// failure seen on loaded whole-tree runs).
func TestStartRemoteControlPairingToleratesSlowHandshakeLikeRust(t *testing.T) {
	requested := make(chan remotecontrol.PairingStartParams, 1)
	base, err := os.MkdirTemp("", "wsuds")
	if err != nil {
		t.Fatalf("MkdirTemp error = %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(base) })
	directory := filepath.Join(base, "private")
	if err := codexuds.PreparePrivateSocketDirectory(directory); err != nil {
		t.Fatalf("PreparePrivateSocketDirectory error = %v", err)
	}
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
		ctx := r.Context()
		serveControlExchange(t, ctx, conn, "initialize", map[string]any{"userAgent": "codex/0.0.0"})
		serveControlExchange(t, ctx, conn, "initialized", map[string]any{})
		request := readControlRequest(t, ctx, conn)
		var params remotecontrol.PairingStartParams
		if err := json.Unmarshal(mustMarshal(t, request.Params), &params); err != nil {
			t.Errorf("server params decode error = %v", err)
			return
		}
		requested <- params
		writeControlResult(t, ctx, conn, request, map[string]any{
			"pairingCode":       "pairing-code",
			"manualPairingCode": "SLOW-HANDSHAKE",
			"environmentId":     "env-1",
		})
	})}
	go func() {
		_ = server.Serve(&delayedFirstAcceptListener{Listener: listener, delay: 250 * time.Millisecond})
	}()
	t.Cleanup(func() {
		_ = server.Close()
		_ = listener.Close()
	})

	response, err := StartRemoteControlPairingOnSocket(path)
	if err != nil {
		t.Fatalf("StartRemoteControlPairingOnSocket() error = %v", err)
	}
	if response.ManualPairingCode == nil || *response.ManualPairingCode != "SLOW-HANDSHAKE" {
		t.Fatalf("manual pairing code = %v, want SLOW-HANDSHAKE", response.ManualPairingCode)
	}
	select {
	case params := <-requested:
		if !params.ManualCode {
			t.Fatal("pairing request did not ask for a manual code")
		}
	case <-time.After(time.Second):
		t.Fatal("server did not observe the pairing request")
	}
}

// TestStartRemoteControlPairingRejectsInvalidParamsLikeRust covers the upstream
// contract: an invalid-params answer is an error, not a retry without manual
// code parameters.
func TestStartRemoteControlPairingRejectsInvalidParamsLikeRust(t *testing.T) {
	socketPath := startPrivateUnixWebSocketControlServer(t, func(ctx context.Context, conn *websocket.Conn) {
		serveControlExchange(t, ctx, conn, "initialize", map[string]any{"userAgent": "codex/0.0.0"})
		serveControlExchange(t, ctx, conn, "initialized", map[string]any{})
		request := readControlRequest(t, ctx, conn)
		payload, err := json.Marshal(map[string]any{
			"jsonrpc": "2.0",
			"id":      request.ID,
			"error":   map[string]any{"code": InvalidParamsErrorCode, "message": "invalid params"},
		})
		if err != nil {
			t.Errorf("encode error response: %v", err)
			return
		}
		if err := conn.Write(ctx, websocket.MessageText, payload); err != nil {
			t.Errorf("write error response: %v", err)
		}
	})

	_, err := StartRemoteControlPairingOnSocket(socketPath)
	if err == nil || !strings.Contains(err.Error(), "remoteControl/pairing/start rejected manual pairing parameters") {
		t.Fatalf("pairing error = %v, want the invalid-parameters message", err)
	}
}

func mustMarshal(t *testing.T, value any) []byte {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal error = %v", err)
	}
	return encoded
}
