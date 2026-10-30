package execserver

import (
	"context"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

// stubLookupHost swaps the package host resolver for one test so the loopback
// DNS restriction can be exercised without touching real name resolution.
func stubLookupHost(t *testing.T, addresses []string) {
	t.Helper()
	original := lookupHost
	lookupHost = func(context.Context, string) ([]string, error) {
		return append([]string(nil), addresses...), nil
	}
	t.Cleanup(func() { lookupHost = original })
}

// TestLoopbackWebSocketAddressesKeepOnlyLoopbackLikeRust pins the Rust
// `loopback_addresses` filter: mixed DNS answers keep only loopback peers.
func TestLoopbackWebSocketAddressesKeepOnlyLoopbackLikeRust(t *testing.T) {
	stubLookupHost(t, []string{"10.1.2.3", "127.0.0.2", "::1"})
	addresses, err := loopbackWebSocketAddresses(context.Background(), "ws://localhost:4321/exec")
	if err != nil {
		t.Fatalf("loopbackWebSocketAddresses() error = %v", err)
	}
	want := []string{"127.0.0.2:4321", "[::1]:4321"}
	if strings.Join(addresses, ",") != strings.Join(want, ",") {
		t.Fatalf("loopback addresses = %#v, want %#v", addresses, want)
	}
}

// TestLoopbackWebSocketAddressesRejectNonLoopbackResolutionLikeRust pins the
// permission error Rust raises when no answer is loopback.
func TestLoopbackWebSocketAddressesRejectNonLoopbackResolutionLikeRust(t *testing.T) {
	stubLookupHost(t, []string{"10.1.2.3"})
	_, err := loopbackWebSocketAddresses(context.Background(), "ws://localhost:4321/exec")
	if err == nil || !strings.Contains(err.Error(), "must resolve to a loopback address") {
		t.Fatalf("loopbackWebSocketAddresses() error = %v, want the loopback rejection", err)
	}
}

// TestLoopbackDirectWebSocketClientBypassesProxyLikeRust pins that the direct
// loopback path ignores configured proxy settings.
func TestLoopbackDirectWebSocketClientBypassesProxyLikeRust(t *testing.T) {
	stubLookupHost(t, []string{"127.0.0.2"})
	client, err := loopbackDirectWebSocketClient(context.Background(), "ws://localhost:4321/exec", nil)
	if err != nil {
		t.Fatalf("loopbackDirectWebSocketClient() error = %v", err)
	}
	transport, ok := client.Transport.(*http.Transport)
	if !ok || transport.Proxy != nil {
		t.Fatalf("loopback transport = %#v, want a proxy-free transport", client.Transport)
	}
}

// TestDialClientWithOptionsRejectsNonLoopbackHeaderDestinationLikeRust proves
// the guard also runs inside the client dial path before any connection.
func TestDialClientWithOptionsRejectsNonLoopbackHeaderDestinationLikeRust(t *testing.T) {
	stubLookupHost(t, []string{"10.1.2.3"})
	_, err := DialClientWithOptions(context.Background(), "ws://localhost:9/exec", DialClientOptions{
		ClientName:  "executor-header-test",
		HTTPHeaders: http.Header{"Authorization": []string{"Bearer test-token"}},
	})
	if err == nil || !strings.Contains(err.Error(), "must resolve to a loopback address") {
		t.Fatalf("DialClientWithOptions() error = %v, want the loopback rejection", err)
	}
}

// TestClientSendsHeadersOverDirectLoopbackConnectionLikeRust proves the
// derived proxy-free client still completes a real header-bearing handshake.
func TestClientSendsHeadersOverDirectLoopbackConnectionLikeRust(t *testing.T) {
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
	_, port, err := net.SplitHostPort(strings.TrimPrefix(serverURL, "ws://"))
	if err != nil {
		t.Fatalf("SplitHostPort(%q) error = %v", serverURL, err)
	}
	client, err := DialClientWithOptions(context.Background(), "ws://localhost:"+port, DialClientOptions{
		ClientName:  "executor-header-test",
		HTTPHeaders: http.Header{"Authorization": []string{"Bearer test-token"}},
	})
	if err != nil {
		t.Fatalf("DialClientWithOptions() error = %v", err)
	}
	client.Close()
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
