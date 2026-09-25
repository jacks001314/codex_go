package execserver

import (
	"context"
	"encoding/base64"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// recordingHeaderTransport records the Authorization header of every WebSocket
// upgrade request so a test can prove the header is attached to the initial
// connection and to reconnects (Rust #47648).
type recordingHeaderTransport struct {
	base http.RoundTripper

	mu          sync.Mutex
	authorizing []string
	urls        []string
}

func (t *recordingHeaderTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	t.mu.Lock()
	t.authorizing = append(t.authorizing, request.Header.Get("Authorization"))
	t.urls = append(t.urls, request.URL.String())
	t.mu.Unlock()
	base := t.base
	if base == nil {
		base = http.DefaultTransport
	}
	return base.RoundTrip(request)
}

func (t *recordingHeaderTransport) seen() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]string(nil), t.authorizing...)
}

// TestClientSendsExecutorHTTPHeadersOnConnectAndReconnectLikeRust mirrors the
// Rust executor bearer-token coverage: the configured Authorization header is
// sent on the direct WebSocket upgrade and again on recoveries, without any
// automatic refresh.
func TestClientSendsExecutorHTTPHeadersOnConnectAndReconnectLikeRust(t *testing.T) {
	serverCtx, cancelServer := context.WithCancel(context.Background())
	defer cancelServer()
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

	transport := &recordingHeaderTransport{}
	client, err := DialClientWithOptions(context.Background(), serverURL, DialClientOptions{
		ClientName:  "executor-header-test",
		HTTPClient:  &http.Client{Transport: transport},
		HTTPHeaders: http.Header{"Authorization": []string{"Bearer test-token"}},
	})
	if err != nil {
		t.Fatalf("DialClientWithOptions() error = %v", err)
	}
	defer client.Close()
	sessionID := client.SessionID()
	if seen := transport.seen(); len(seen) != 1 || seen[0] != "Bearer test-token" {
		t.Fatalf("initial upgrade headers = %#v", seen)
	}

	// Drop the connection and recover: the reconnect must carry the header too.
	client.mu.Lock()
	originalConn := client.conn
	client.mu.Unlock()
	if originalConn == nil {
		t.Fatal("client connection is nil")
	}
	if err := originalConn.CloseNow(); err != nil {
		t.Fatalf("CloseNow() error = %v", err)
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		client.mu.Lock()
		disconnected := client.conn == nil
		client.mu.Unlock()
		if disconnected {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	path := filepath.Join(t.TempDir(), "headers.txt")
	if err := os.WriteFile(path, []byte("authorized"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	callCtx, cancelCall := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelCall()
	response, err := client.FSReadFile(callCtx, &FSReadFileParams{Path: path})
	if err != nil {
		t.Fatalf("FSReadFile() after disconnect error = %v", err)
	}
	data, decodeErr := base64.StdEncoding.DecodeString(response.DataBase64)
	if decodeErr != nil || string(data) != "authorized" {
		t.Fatalf("FSReadFile() data = %q, err = %v", data, decodeErr)
	}
	if client.SessionID() != sessionID {
		t.Fatalf("session id after recovery = %q, want %q", client.SessionID(), sessionID)
	}
	seen := transport.seen()
	if len(seen) < 2 {
		t.Fatalf("upgrade attempts = %#v, want the reconnect to redial", seen)
	}
	for index, value := range seen {
		if value != "Bearer test-token" {
			t.Fatalf("upgrade %d Authorization = %q, want the configured token on every attempt", index, value)
		}
	}

	cancelServer()
	select {
	case err := <-serverDone:
		if err != nil {
			t.Fatalf("exec-server shutdown error = %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("exec-server did not stop")
	}
}

// TestClientRejectsInsecureHeaderConfigurationLikeRust pins the validation
// boundary: headers on a plain remote ws:// endpoint are rejected before any
// connection attempt, so the token never leaves the client in the clear.
func TestClientRejectsInsecureHeaderConfigurationLikeRust(t *testing.T) {
	transport := &recordingHeaderTransport{}
	_, err := DialClientWithOptions(context.Background(), "ws://executor.example/exec", DialClientOptions{
		ClientName:  "executor-header-test",
		HTTPClient:  &http.Client{Transport: transport},
		HTTPHeaders: http.Header{"Authorization": []string{"Bearer test-token"}},
	})
	if err == nil || !strings.Contains(err.Error(), "wss:// or a loopback destination") {
		t.Fatalf("DialClientWithOptions() error = %v, want the secure-transport rejection", err)
	}
	if seen := transport.seen(); len(seen) != 0 {
		t.Fatalf("rejected configuration still dialed: %#v", seen)
	}
}
