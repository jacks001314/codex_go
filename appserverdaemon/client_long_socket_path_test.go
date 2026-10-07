//go:build !windows

package appserverdaemon

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"codex_go/remotecontrol"
)

// longAdvertisedControlSocketForTest mirrors Rust #48772's
// `long_control_socket_paths_connect_to_distinct_daemons` setup: the advertised
// control socket path is a symlink whose own length exceeds the kernel's
// sun_path field while its target is short enough to connect to.
func longAdvertisedControlSocketForTest(t *testing.T, target string) string {
	t.Helper()
	longHome := filepath.Join(t.TempDir(), strings.Repeat("x", 120), "codex-home")
	if err := os.MkdirAll(longHome, 0o700); err != nil {
		t.Fatalf("create long codex home: %v", err)
	}
	advertised := filepath.Join(longHome, "app-server-control.sock")
	if err := os.Symlink(target, advertised); err != nil {
		t.Fatalf("symlink advertised control socket: %v", err)
	}
	if len(advertised) <= 108 {
		t.Fatalf("advertised path %q (%d bytes) does not exceed sun_path", advertised, len(advertised))
	}
	rawConn, rawErr := net.Dial("unix", advertised)
	if rawErr == nil {
		_ = rawConn.Close()
		t.Fatalf("raw dial of the over-long advertised path %q unexpectedly succeeded", advertised)
	}
	t.Logf("raw dial error (expected): %v", rawErr)
	return advertised
}

func TestControlSocketClientConnectsThroughLongAdvertisedSymlinkPath(t *testing.T) {
	shortPath := startUnixWebSocketControlServer(t, func(ctx context.Context, conn *websocket.Conn) {
		serveControlExchange(t, ctx, conn, "initialize", map[string]any{"userAgent": "codex/0.0.0"})
		serveControlExchange(t, ctx, conn, "initialized", map[string]any{})
		serveControlExchange(t, ctx, conn, "remoteControl/enable", map[string]any{
			"status":         remotecontrol.StatusConnected,
			"serverName":     "test-machine",
			"installationId": "install-1",
			"environmentId":  "env-1",
		})
	})
	advertised := longAdvertisedControlSocketForTest(t, shortPath)
	status, err := EnableRemoteControlOnSocket(advertised, 5*time.Second, 25*time.Millisecond)
	if err != nil {
		t.Fatalf("EnableRemoteControlOnSocket(%q) error = %v", advertised, err)
	}
	if status.Status != remotecontrol.StatusConnected {
		t.Fatalf("status = %#v", status)
	}
}

func TestManualUpdaterConnectsThroughLongAdvertisedSymlinkPath(t *testing.T) {
	shortPath := filepath.Join(t.TempDir(), "updater.sock")
	listener, err := net.Listen("unix", shortPath)
	if err != nil {
		t.Fatalf("listen %s: %v", shortPath, err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	accepted := make(chan struct{}, 1)
	go func() {
		conn, acceptErr := listener.Accept()
		if acceptErr != nil {
			return
		}
		accepted <- struct{}{}
		_ = conn.Close()
	}()
	advertised := longAdvertisedControlSocketForTest(t, shortPath)
	conn, err := connectManualUpdaterSocket(advertised, 2*time.Second)
	if err != nil {
		t.Fatalf("connectManualUpdaterSocket(%q) error = %v", advertised, err)
	}
	_ = conn.Close()
	select {
	case <-accepted:
	case <-time.After(2 * time.Second):
		t.Fatalf("no connection reached the updater socket through %q", advertised)
	}
}
