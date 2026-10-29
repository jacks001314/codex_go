//go:build !windows

package appserver

import (
	"context"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// serveControlSocketForTest binds a plain unix listener and serves it through
// serveUnixSocketHTTP, which is the layer Rust app-server-transport exercises
// in its control socket tests.
func serveControlSocketForTest(t *testing.T, access DaemonShutdownAccess, shutdown func()) string {
	t.Helper()
	socketPath := filepath.Join(t.TempDir(), "control.sock")
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("Listen(unix) error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	codexHome := t.TempDir()
	go func() {
		done <- serveUnixSocketHTTP(ctx, listener, func() *RuntimeRouter {
			return NewUnixSocketRouter(codexHome)
		}, access, shutdown, false)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("serveUnixSocketHTTP error = %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Error("serveUnixSocketHTTP did not stop after cancellation")
		}
	})
	return socketPath
}

// dialControlSocketAt upgrades a connection to requestPath the way the real
// clients do. A non-101 answer is returned with its response instead of a
// connection, matching how Rust surfaces a rejected upgrade.
func dialControlSocketAt(t *testing.T, socketPath string, requestPath string) (*websocket.Conn, *http.Response, error) {
	t.Helper()
	transport := &http.Transport{
		DialContext: func(ctx context.Context, network string, addr string) (net.Conn, error) {
			var dialer net.Dialer
			return dialer.DialContext(ctx, "unix", socketPath)
		},
	}
	deadline := time.Now().Add(15 * time.Second)
	for {
		conn, response, err := websocket.Dial(context.Background(), "ws://localhost"+requestPath, &websocket.DialOptions{
			HTTPClient: &http.Client{Transport: transport},
		})
		if err == nil {
			return conn, response, nil
		}
		if response != nil {
			return nil, response, err
		}
		if time.Now().After(deadline) {
			t.Fatalf("control socket %s never accepted a websocket connection: %v", socketPath, err)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func assertAdvertisedMessageLimit(t *testing.T, response *http.Response) {
	t.Helper()
	if response == nil {
		t.Fatal("websocket upgrade response is nil")
	}
	if response.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("upgrade status = %d, want 101", response.StatusCode)
	}
	raw := response.Header.Get(maxUnfragmentedMessageBytesHeader)
	if raw == "" {
		t.Fatalf("upgrade response is missing %s", maxUnfragmentedMessageBytesHeader)
	}
	advertised, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		t.Fatalf("advertised message limit %q is not numeric: %v", raw, err)
	}
	if advertised != DefaultWebSocketMaxMessageSize {
		t.Fatalf("advertised message limit = %d, want %d", advertised, DefaultWebSocketMaxMessageSize)
	}
}

// TestControlSocketAdvertisesUnfragmentedMessageLimitLikeRust pins Rust #46548:
// an accepted control socket upgrade advertises the single-frame cap so clients
// can reject oversized requests before the socket closes.
func TestControlSocketAdvertisesUnfragmentedMessageLimitLikeRust(t *testing.T) {
	socketPath := serveControlSocketForTest(t, DaemonShutdownDisabled, nil)
	conn, response, err := dialControlSocketAt(t, socketPath, "/rpc")
	if err != nil {
		t.Fatalf("websocket upgrade error = %v", err)
	}
	defer func() { _ = conn.Close(websocket.StatusNormalClosure, "") }()
	assertAdvertisedMessageLimit(t, response)
}

// TestManagedShutdownSocketAdvertisesUnfragmentedMessageLimitLikeRust covers the
// managed daemon shutdown path: Rust inserts the header after the access check,
// so a successful shutdown upgrade advertises it too.
func TestManagedShutdownSocketAdvertisesUnfragmentedMessageLimitLikeRust(t *testing.T) {
	shutdown := make(chan struct{}, 1)
	socketPath := serveControlSocketForTest(t, DaemonShutdownManaged, func() {
		select {
		case shutdown <- struct{}{}:
		default:
		}
	})
	conn, response, err := dialControlSocketAt(t, socketPath, DaemonShutdownPath)
	if err != nil {
		t.Fatalf("websocket upgrade error = %v", err)
	}
	defer func() { _ = conn.Close(websocket.StatusNormalClosure, "") }()
	assertAdvertisedMessageLimit(t, response)

	pid := strconv.Itoa(os.Getpid())
	writeCtx, cancelWrite := context.WithTimeout(context.Background(), 5*time.Second)
	err = conn.Write(writeCtx, websocket.MessageText, []byte(pid))
	cancelWrite()
	if err != nil {
		t.Fatalf("write pid error = %v", err)
	}
	readCtx, cancelRead := context.WithTimeout(context.Background(), 5*time.Second)
	_, data, err := conn.Read(readCtx)
	cancelRead()
	if err != nil {
		t.Fatalf("read pid echo error = %v", err)
	}
	if string(data) != pid {
		t.Fatalf("pid echo = %q, want %q", string(data), pid)
	}
	writeCtx, cancelWrite = context.WithTimeout(context.Background(), 5*time.Second)
	err = conn.Write(writeCtx, websocket.MessageText, []byte("ack"))
	cancelWrite()
	if err != nil {
		t.Fatalf("write ack error = %v", err)
	}
	select {
	case <-shutdown:
	case <-time.After(5 * time.Second):
		t.Fatal("managed shutdown was not requested after the handshake")
	}
}

// TestRejectedShutdownSocketOmitsUnfragmentedMessageLimitLikeRust pins the other
// half of #46548: a refused shutdown upgrade keeps the bare 403 response and
// never advertises the limit.
func TestRejectedShutdownSocketOmitsUnfragmentedMessageLimitLikeRust(t *testing.T) {
	socketPath := serveControlSocketForTest(t, DaemonShutdownDisabled, nil)
	conn, response, err := dialControlSocketAt(t, socketPath, DaemonShutdownPath)
	if conn != nil {
		_ = conn.Close(websocket.StatusNormalClosure, "")
		t.Fatal("unmanaged server accepted the shutdown upgrade")
	}
	if err == nil {
		t.Fatal("unmanaged server returned nil error for the shutdown upgrade")
	}
	if response == nil {
		t.Fatal("unmanaged server returned no response")
	}
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("rejected upgrade status = %d, want 403", response.StatusCode)
	}
	if got := response.Header.Get(maxUnfragmentedMessageBytesHeader); got != "" {
		t.Fatalf("rejected upgrade advertised %s=%q", maxUnfragmentedMessageBytesHeader, got)
	}
}
