package realtime

import (
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/coder/websocket"

	"codex_go/network"
)

// chatGPTRoutedClient dials the local test server for every host while trusting
// its certificate, so a handshake to a real ChatGPT hostname exercises the
// shared cookie scope without talking to the network.
func chatGPTRoutedClient(server *httptest.Server) *http.Client {
	transport := server.Client().Transport.(*http.Transport).Clone()
	transport.DialContext = func(ctx context.Context, networkName, address string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, networkName, server.Listener.Addr().String())
	}
	transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	return &http.Client{Transport: transport}
}

// TestRealtimeHandshakeSharesChatGPTCookiesLikeRust mirrors Rust #46506 for the
// realtime transport: a secure handshake reuses the shared routing cookie, a
// rejected upgrade refreshes it, and the next handshake sends the refreshed
// value instead of the stale one.
func TestRealtimeHandshakeSharesChatGPTCookiesLikeRust(t *testing.T) {
	const host = "realtime-cookies.chatgpt.com"
	target, err := url.Parse("https://" + host + "/")
	if err != nil {
		t.Fatal(err)
	}
	network.SharedChatGPTCookieJar().SetCookies(target, []*http.Cookie{{Name: "__oailb", Value: "routed", Path: "/", Secure: true}})
	t.Cleanup(func() {
		network.SharedChatGPTCookieJar().SetCookies(target, []*http.Cookie{{Name: "__oailb", Value: "", Path: "/", MaxAge: -1}})
	})

	seen := make(chan string, 4)
	rejected := false
	server := httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		seen <- request.Header.Get("Cookie")
		if !rejected {
			rejected = true
			http.SetCookie(response, &http.Cookie{Name: "__oailb", Value: "refreshed", Path: "/", Secure: true})
			response.WriteHeader(http.StatusForbidden)
			return
		}
		conn, err := websocket.Accept(response, request, nil)
		if err != nil {
			return
		}
		defer conn.CloseNow()
		<-request.Context().Done()
	}))
	defer server.Close()

	backend := &TransportBackendConfig{
		WebsocketBaseURL: "https://" + host,
		HTTPClient:       chatGPTRoutedClient(server),
	}

	// The first upgrade is rejected, but the routing cookie rides on the
	// handshake and the rejection's refresh is retained.
	manager := NewManager()
	manager.SetTransportBackend(backend)
	if _, _, err := manager.Start(&StartParams{ThreadID: "thread-cookie", OutputModality: OutputAudio}); err == nil {
		t.Fatal("rejected realtime upgrade unexpectedly succeeded")
	}
	if got := <-seen; got != "__oailb=routed" {
		t.Fatalf("first handshake cookie = %q, want __oailb=routed", got)
	}

	// The next connector sees the refreshed routing cookie.
	manager = NewManager()
	manager.SetTransportBackend(backend)
	if _, _, err := manager.Start(&StartParams{ThreadID: "thread-cookie", OutputModality: OutputAudio}); err != nil {
		t.Fatalf("start realtime with refreshed cookie: %v", err)
	}
	if got := <-seen; got != "__oailb=refreshed" {
		t.Fatalf("refreshed handshake cookie = %q, want __oailb=refreshed", got)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := manager.Shutdown(ctx); err != nil {
		t.Fatalf("shutdown realtime manager: %v", err)
	}
}
