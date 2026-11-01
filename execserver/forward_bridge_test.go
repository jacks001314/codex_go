package execserver

import (
	"bufio"
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// Rust's forwarder dials the destination once per relay stream and copies each
// payload in both directions unchanged (codex-rs/exec-server/src/forward.rs).
func TestServeForwardedStreamBridgesPayloadsLikeRust(t *testing.T) {
	received := make(chan string, 1)
	relay := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.CloseNow()
		for {
			messageType, payload, err := conn.Read(r.Context())
			if err != nil {
				return
			}
			received <- string(payload)
			if err := conn.Write(r.Context(), messageType, payload); err != nil {
				return
			}
		}
	}))
	defer relay.Close()

	server := NewServer()
	server.SetForwardDestination("ws" + strings.TrimPrefix(relay.URL, "http"))

	clientConn, relayConn := net.Pipe()
	defer clientConn.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- server.serveForwardedStream(ctx, relayConn) }()

	const payload = `{"jsonrpc":"2.0","id":1,"method":"ping"}`
	if _, err := clientConn.Write([]byte(payload + "\n")); err != nil {
		t.Fatalf("write relay payload: %v", err)
	}
	reader := bufio.NewReader(clientConn)
	line, err := readRemoteRelayJSONLine(reader)
	if err != nil {
		t.Fatalf("read forwarded payload: %v", err)
	}
	if string(line) != payload {
		t.Fatalf("forwarded payload = %q, want %q", string(line), payload)
	}
	select {
	case got := <-received:
		if got != payload {
			t.Fatalf("destination payload = %q, want %q", got, payload)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("destination never received the relay payload")
	}

	_ = clientConn.Close()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("serveForwardedStream did not stop after the relay stream closed")
	}
}

// The forwarder refuses to run without a destination, mirroring Rust's
// requirement that `forward` carries --connect plus a registered remote.
func TestServeForwardedStreamRequiresDestination(t *testing.T) {
	server := NewServer()
	clientConn, relayConn := net.Pipe()
	defer clientConn.Close()
	defer relayConn.Close()
	err := server.serveForwardedStream(context.Background(), relayConn)
	if err == nil || !strings.Contains(err.Error(), "not configured") {
		t.Fatalf("serveForwardedStream() error = %v, want the missing destination", err)
	}
}
