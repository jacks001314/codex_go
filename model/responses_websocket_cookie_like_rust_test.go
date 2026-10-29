package model

import (
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/coder/websocket"

	"codex_go/network"
)

// chatGPTCookieRoutedClient dials the local test server for every host while
// trusting its certificate, so a handshake to a ChatGPT hostname exercises the
// shared cookie scope without talking to the network.
func chatGPTCookieRoutedClient(server *httptest.Server) *http.Client {
	transport := server.Client().Transport.(*http.Transport).Clone()
	transport.DialContext = func(ctx context.Context, networkName, address string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, networkName, server.Listener.Addr().String())
	}
	transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	return &http.Client{Transport: transport}
}

// TestResponsesWebsocketSharesChatGPTCookiesLikeRust mirrors Rust #46506 for the
// Responses WebSocket client: the handshake reuses the shared routing cookie, a
// rejected upgrade refreshes it, and the next connector sends the refreshed
// value instead of the stale one.
func TestResponsesWebsocketSharesChatGPTCookiesLikeRust(t *testing.T) {
	const host = "model-cookies.chatgpt.com"
	target, err := url.Parse("https://" + host + "/")
	if err != nil {
		t.Fatal(err)
	}
	network.SharedChatGPTCookieJar().SetCookies(target, []*http.Cookie{{Name: "__oailb", Value: "routed", Path: "/", Secure: true}})
	t.Cleanup(func() {
		network.SharedChatGPTCookieJar().SetCookies(target, []*http.Cookie{{
			Name: "__oailb", Value: "", Path: "/", MaxAge: -1,
		}})
	})

	seen := make(chan string, 4)
	rejected := false
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		seen <- request.Header.Get("Cookie")
		if !rejected {
			rejected = true
			http.SetCookie(w, &http.Cookie{Name: "__oailb", Value: "refreshed", Path: "/", Secure: true})
			w.WriteHeader(http.StatusForbidden)
			return
		}
		conn, err := websocket.Accept(w, request, nil)
		if err != nil {
			t.Errorf("Accept() error = %v", err)
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "")
		if _, _, err := conn.Read(request.Context()); err != nil {
			return
		}
		response := `{"type":"response.completed","response":{"id":"resp-cookie","output":[{"id":"msg-cookie","type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}]}}`
		_ = conn.Write(request.Context(), websocket.MessageText, []byte(response))
	}))
	defer server.Close()

	options := &ResponsesAgentOptions{
		Provider:           &APIProvider{BaseURL: "https://" + host},
		HTTPClient:         chatGPTCookieRoutedClient(server),
		SupportsWebsockets: true,
	}
	request := &AgentRequest{Model: "gpt-test", Prompt: "hello", ThreadID: "thread-cookie", TurnID: "turn-cookie"}

	runner := NewResponsesAgentRunner(options)
	if _, err := runner.RunWebSocket(context.Background(), request); err == nil {
		t.Fatal("rejected websocket upgrade unexpectedly succeeded")
	}
	if got := <-seen; got != "__oailb=routed" {
		t.Fatalf("first handshake cookie = %q, want __oailb=routed", got)
	}

	runner = NewResponsesAgentRunner(options)
	response, err := runner.RunWebSocket(context.Background(), request)
	if err != nil || response == nil {
		t.Fatalf("RunWebSocket response=%#v err=%v", response, err)
	}
	if got := <-seen; got != "__oailb=refreshed" {
		t.Fatalf("refreshed handshake cookie = %q, want __oailb=refreshed", got)
	}
}
