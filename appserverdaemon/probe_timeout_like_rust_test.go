package appserverdaemon

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/coder/websocket"

	"codex_go/codexuds"
)

// slowInitializeDelay models a daemon that answers the WebSocket upgrade
// immediately but spends time before the initialize response, which is what a
// per-connection router construction does under load.
const slowInitializeDelay = 500 * time.Millisecond

// TestControlSocketProbeTimeoutMatchesRust pins the probe budget to Rust
// client::probe's CONTROL_SOCKET_RESPONSE_TIMEOUT. A shorter budget makes a
// healthy daemon look unready whenever router construction outlasts it, and the
// foreground start then fails with "timed out waiting for the app-server
// control socket".
func TestControlSocketProbeTimeoutMatchesRust(t *testing.T) {
	if ControlSocketProbeTimeout != ControlSocketResponseTimeout {
		t.Fatalf("ControlSocketProbeTimeout = %v, want the control-socket response budget %v", ControlSocketProbeTimeout, ControlSocketResponseTimeout)
	}
	if ControlSocketProbeTimeout != 2*time.Second {
		t.Fatalf("ControlSocketProbeTimeout = %v, want Rust CONTROL_SOCKET_RESPONSE_TIMEOUT (2s)", ControlSocketProbeTimeout)
	}
}

// TestProbeAppServerVersionWaitsForSlowInitializeLikeRust covers the daemon
// that upgrades the control socket quickly but answers initialize only after a
// few hundred milliseconds.
func TestProbeAppServerVersionWaitsForSlowInitializeLikeRust(t *testing.T) {
	socketPath := startDelayedInitializeControlServer(t)

	shortBudget := 200 * time.Millisecond
	if _, err := ProbeAppServerVersionOnSocket(socketPath, shortBudget); err == nil {
		t.Fatalf("a %v probe answered the delayed initialize; the fixture does not exercise the regression", shortBudget)
	}

	version, err := ProbeAppServerVersionOnSocket(socketPath, ControlSocketProbeTimeout)
	if err != nil {
		t.Fatalf("ProbeAppServerVersionOnSocket error = %v", err)
	}
	if version != "9.9.9" {
		t.Fatalf("version = %q, want 9.9.9", version)
	}
}

// startDelayedInitializeControlServer serves the control socket and delays the
// initialize response by slowInitializeDelay.
func startDelayedInitializeControlServer(t *testing.T) string {
	t.Helper()
	// Keep the AF_UNIX path short enough for Windows' sun_path limit.
	directory := filepath.Join(os.TempDir(), fmt.Sprintf("cdxp%d-%d", os.Getpid(), time.Now().UnixNano()))
	if err := codexuds.PreparePrivateSocketDirectory(directory); err != nil {
		t.Fatalf("PreparePrivateSocketDirectory error = %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(directory) })
	socketPath := filepath.Join(directory, "control.sock")
	listener, err := net.Listen("unix", socketPath)
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
		_, data, err := conn.Read(ctx)
		if err != nil {
			return
		}
		var request JSONRPCRequest
		if err := json.Unmarshal(data, &request); err != nil || request.Method != "initialize" {
			return
		}
		time.Sleep(slowInitializeDelay)
		payload, err := json.Marshal(map[string]any{
			"jsonrpc": "2.0",
			"id":      request.ID,
			"result":  map[string]any{"userAgent": "codex_app_server/9.9.9"},
		})
		if err != nil {
			return
		}
		if err := conn.Write(ctx, websocket.MessageText, payload); err != nil {
			return
		}
		// The probe sends the initialized notification after the response.
		_, _, _ = conn.Read(ctx)
	})}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() {
		_ = server.Close()
		_ = listener.Close()
	})
	return socketPath
}
