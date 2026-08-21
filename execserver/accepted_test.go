package execserver

import (
	"context"
	"strings"
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
