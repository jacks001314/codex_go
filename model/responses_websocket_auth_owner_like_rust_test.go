package model

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/coder/websocket"
)

var wsAuthOwnerCompletion = []string{
	`{"type":"response.created","response":{"id":"resp-auth-owner"}}`,
	`{"type":"response.output_item.done","item":{"id":"msg-auth-owner","type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}}`,
	`{"type":"response.completed","response":{"id":"resp-auth-owner","output":[{"id":"msg-auth-owner","type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}}`,
}

// TestResponsesWebsocketReconnectsAfterAuthOwnerChangeLikeRust mirrors Rust
// #60436 (websocket_sends_after_external_auth_changes_during_request_setup):
// credentials changed between two turns must produce a fresh handshake carrying
// the current bearer token, because a socket opened for the previous owner
// cannot send the next request.
func TestResponsesWebsocketReconnectsAfterAuthOwnerChangeLikeRust(t *testing.T) {
	handshakes := make(chan string, 4)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if !isResponsesWebSocketUpgrade(request) {
			http.Error(w, "unexpected HTTP request", http.StatusInternalServerError)
			return
		}
		handshakes <- request.Header.Get("Authorization")
		conn, err := websocket.Accept(w, request, nil)
		if err != nil {
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "done")
		for {
			if _, _, err := conn.Read(request.Context()); err != nil {
				return
			}
			for _, event := range wsAuthOwnerCompletion {
				if err := conn.Write(request.Context(), websocket.MessageText, []byte(event)); err != nil {
					return
				}
			}
		}
	}))
	defer server.Close()

	first := testAuthSnapshotWithToken("token-a")
	firstHeaders, err := AuthHeadersFromAuth(*first)
	if err != nil {
		t.Fatalf("AuthHeadersFromAuth(first) error = %v", err)
	}
	runner := NewResponsesAgentRunner(&ResponsesAgentOptions{
		Provider:                &APIProvider{BaseURL: server.URL},
		Stream:                  true,
		SupportsWebsockets:      true,
		WebsocketConnectTimeout: 2 * time.Second,
		HTTPClient:              server.Client(),
		Auth:                    &firstHeaders,
		AuthSnapshot:            first,
	})
	if _, err := runner.Run(context.Background(), &AgentRequest{Prompt: "first", Model: "gpt-test"}); err != nil {
		t.Fatalf("first Run() error = %v", err)
	}

	second := testAuthSnapshotWithToken("token-b")
	secondHeaders, err := AuthHeadersFromAuth(*second)
	if err != nil {
		t.Fatalf("AuthHeadersFromAuth(second) error = %v", err)
	}
	runner.Auth = &secondHeaders
	runner.AuthSnapshot = second
	if _, err := runner.Run(context.Background(), &AgentRequest{Prompt: "second", Model: "gpt-test"}); err != nil {
		t.Fatalf("second Run() error = %v", err)
	}

	nextHandshake := func() string {
		select {
		case header := <-handshakes:
			return header
		case <-time.After(5 * time.Second):
			t.Fatal("timed out waiting for a Responses WebSocket handshake")
			return ""
		}
	}
	got := []string{nextHandshake(), nextHandshake()}
	want := []string{"Bearer token-a", "Bearer token-b"}
	if got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("handshake Authorization = %v, want %v", got, want)
	}
	select {
	case extra := <-handshakes:
		t.Fatalf("unexpected extra handshake carrying %q", extra)
	default:
	}
}
