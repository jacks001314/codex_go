package appserver

import (
	"context"
	"net"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// dialControlSocketWebSocket upgrades a control-socket connection the way the
// real clients do: raw unix dial, then the `ws://localhost/rpc` handshake (Rust
// app-server-client::connect_unix_socket_endpoint).
func dialControlSocketWebSocket(t *testing.T, socketPath string) *websocket.Conn {
	t.Helper()
	transport := &http.Transport{
		DialContext: func(ctx context.Context, network string, addr string) (net.Conn, error) {
			var dialer net.Dialer
			return dialer.DialContext(ctx, "unix", socketPath)
		},
	}
	deadline := time.Now().Add(15 * time.Second)
	for {
		conn, _, err := websocket.Dial(context.Background(), "ws://localhost/rpc", &websocket.DialOptions{
			HTTPClient: &http.Client{Transport: transport},
		})
		if err == nil {
			t.Cleanup(func() { _ = conn.Close(websocket.StatusNormalClosure, "") })
			return conn
		}
		if time.Now().After(deadline) {
			t.Fatalf("control socket %s never accepted a websocket connection: %v", socketPath, err)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestUnixSocketPathDefaultAndExplicit(t *testing.T) {
	home := filepath.Join("tmp", "codex-home")
	defaultPath, err := UnixSocketPath("unix://", home)
	if err != nil {
		t.Fatalf("UnixSocketPath default error = %v", err)
	}
	if defaultPath != AppServerControlSocketPath(home) {
		t.Fatalf("default path = %q, want %q", defaultPath, AppServerControlSocketPath(home))
	}

	explicit, err := UnixSocketPath("unix://tmp/codex.sock", home)
	if err != nil {
		t.Fatalf("UnixSocketPath explicit error = %v", err)
	}
	wantExplicit, err := filepath.Abs(filepath.Join("tmp", "codex.sock"))
	if err != nil {
		t.Fatalf("Abs error = %v", err)
	}
	if explicit != filepath.Clean(wantExplicit) {
		t.Fatalf("explicit path = %q", explicit)
	}
}

func TestUnixSocketPathRejectsUnsupportedScheme(t *testing.T) {
	_, err := UnixSocketPath("tcp://127.0.0.1:0", t.TempDir())
	if err == nil {
		t.Fatal("UnixSocketPath returned nil error, want unsupported scheme")
	}
}
