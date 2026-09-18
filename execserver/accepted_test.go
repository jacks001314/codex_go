package execserver

import (
	"context"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func startAcceptedTestServer(t *testing.T) (string, context.CancelFunc) {
	t.Helper()
	serverCtx, cancelServer := context.WithCancel(context.Background())
	urlCh := make(chan string, 1)
	serverDone := make(chan error, 1)
	go func() {
		serverDone <- NewServer().ServeTransport(serverCtx, "ws://127.0.0.1:0", nil, &execServerURLChannelWriter{url: urlCh})
	}()
	var serverURL string
	select {
	case serverURL = <-urlCh:
	case <-time.After(3 * time.Second):
		t.Fatal("exec-server URL was not reported")
	}
	t.Cleanup(func() {
		cancelServer()
		select {
		case err := <-serverDone:
			if err != nil {
				t.Fatalf("exec-server shutdown error = %v", err)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("exec-server did not stop")
		}
	})
	return serverURL, cancelServer
}

func TestConnectAcceptedClientInitializesAndReplacesLikeRust(t *testing.T) {
	serverURL, _ := startAcceptedTestServer(t)

	host, _, err := websocket.Dial(context.Background(), serverURL, nil)
	if err != nil {
		t.Fatalf("host Dial() error = %v", err)
	}
	defer host.Close(websocket.StatusNormalClosure, "")
	client, err := ConnectAcceptedClient(host, "accepted-test")
	if err != nil {
		t.Fatalf("ConnectAcceptedClient() error = %v", err)
	}
	defer client.Close()
	sessionID := client.SessionID()
	if strings.TrimSpace(sessionID) == "" {
		t.Fatal("accepted client has no session id")
	}

	// A replacement connection resumes the same session.
	replacement, _, err := websocket.Dial(context.Background(), serverURL, nil)
	if err != nil {
		t.Fatalf("replacement Dial() error = %v", err)
	}
	defer replacement.Close(websocket.StatusNormalClosure, "")
	if err := client.ReplaceAcceptedConnection(context.Background(), replacement); err != nil {
		t.Fatalf("ReplaceAcceptedConnection() error = %v", err)
	}
	if got := client.SessionID(); got != sessionID {
		t.Fatalf("session id after replacement = %q, want %q", got, sessionID)
	}
}

// scriptedAttachConnection replays canned handshake responses so the
// replacement attach can be driven deterministically.
type scriptedAttachConnection struct {
	mu        sync.Mutex
	responses [][]byte
	index     int
	closed    bool
}

func (c *scriptedAttachConnection) Read(context.Context) ([]byte, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.index >= len(c.responses) {
		return nil, io.EOF
	}
	data := c.responses[c.index]
	c.index++
	return data, nil
}

func (c *scriptedAttachConnection) Write(context.Context, []byte) error { return nil }

func (c *scriptedAttachConnection) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed = true
	return nil
}

func (c *scriptedAttachConnection) CloseNow() error { return c.Close() }

func (c *scriptedAttachConnection) isClosed() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closed
}

// TestAcceptedReplacementRetriesAlreadyAttachedLikeRust mirrors Rust
// client_recovery::is_retryable_recovery_error: retiring the old transport is
// asynchronous on the server, so a replacement attach that resumes the session
// can be rejected as still attached. Rust retries the code, and Go must too
// instead of failing the host's handoff and losing its socket.
func TestAcceptedReplacementRetriesAlreadyAttachedLikeRust(t *testing.T) {
	conn := &scriptedAttachConnection{responses: [][]byte{
		[]byte(`{"jsonrpc":"2.0","id":1,"error":{"code":-32010,"message":"session session-1 is already attached to another connection"}}`),
		[]byte(`{"jsonrpc":"2.0","id":1,"result":{"sessionId":"session-1"}}`),
	}}
	initialized, err := attachAcceptedReplacement(context.Background(), conn, "accepted-test", "session-1", nil)
	if err != nil {
		t.Fatalf("attachAcceptedReplacement() error = %v", err)
	}
	if initialized == nil || initialized.SessionID != "session-1" {
		t.Fatalf("initialized = %#v, want the resumed session", initialized)
	}
	if conn.isClosed() {
		t.Fatal("a retried replacement must keep its host-supplied socket")
	}
}

// TestAcceptedReplacementSurfacesNonRetryableAttachErrorsLikeRust pins that
// only the already-attached code is retried: any other attach failure still
// fails the handoff and releases the socket.
func TestAcceptedReplacementSurfacesNonRetryableAttachErrorsLikeRust(t *testing.T) {
	conn := &scriptedAttachConnection{responses: [][]byte{
		[]byte(`{"jsonrpc":"2.0","id":1,"error":{"code":-32600,"message":"unknown session id session-1"}}`),
	}}
	_, err := attachAcceptedReplacement(context.Background(), conn, "accepted-test", "session-1", nil)
	if err == nil || !strings.Contains(err.Error(), "unknown session id session-1") {
		t.Fatalf("attachAcceptedReplacement() error = %v, want the server error", err)
	}
	if !conn.isClosed() {
		t.Fatal("a non-retryable attach failure must release the socket")
	}
}

func TestAcceptedConnectionRejectsMisuseLikeRust(t *testing.T) {
	serverURL, _ := startAcceptedTestServer(t)
	host, _, err := websocket.Dial(context.Background(), serverURL, nil)
	if err != nil {
		t.Fatalf("host Dial() error = %v", err)
	}
	defer host.Close(websocket.StatusNormalClosure, "")
	client, err := ConnectAcceptedClient(host, "accepted-test")
	if err != nil {
		t.Fatalf("ConnectAcceptedClient() error = %v", err)
	}
	defer client.Close()

	// A regular dialed client is not an accepted connection.
	dialed, err := DialClient(context.Background(), serverURL, "dialed-test")
	if err != nil {
		t.Fatalf("DialClient() error = %v", err)
	}
	defer dialed.Close()
	extra, _, err := websocket.Dial(context.Background(), serverURL, nil)
	if err != nil {
		t.Fatalf("extra Dial() error = %v", err)
	}
	defer extra.Close(websocket.StatusNormalClosure, "")
	if err := dialed.ReplaceAcceptedConnection(context.Background(), extra); err == nil || !strings.Contains(err.Error(), "only an accepted exec-server connection") {
		t.Fatalf("non-accepted replacement error = %v", err)
	}
}
