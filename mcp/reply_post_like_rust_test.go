package mcp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestReplyPostBodyCannotResolveRequestsLikeRust pins Rust #63075: a reply POST
// is answered with an acknowledgement only; a JSON-RPC response or error body
// is rejected instead of being dispatched for one of our own request IDs.
func TestReplyPostBodyCannotResolveRequestsLikeRust(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rejection := `{"jsonrpc":"2.0","id":7,"error":{"code":-32600,"message":"unknown request"}}`
		switch r.URL.Path {
		case "/ack":
			w.WriteHeader(http.StatusAccepted)
		case "/empty":
			w.WriteHeader(http.StatusNoContent)
		case "/json":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(rejection))
		case "/sse":
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte("event: message\ndata: " + rejection + "\n\n"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := newMCPHTTPClient(&ServerConfig{URL: server.URL})
	reply := map[string]any{"jsonrpc": "2.0", "id": 7, "result": map[string]any{"action": "accept"}}
	for _, path := range []string{"/json", "/sse"} {
		if err := client.doClientResponse(context.Background(), reply, "same-session"); err == nil {
			t.Fatalf("%s reply body was accepted; a response must not answer a request", path)
		}
	}
	for _, path := range []string{"/ack", "/empty"} {
		client.config.URL = server.URL + path
		if err := client.doClientResponse(context.Background(), reply, "same-session"); err != nil {
			t.Fatalf("%s acknowledgement rejected: %v", path, err)
		}
	}
}
