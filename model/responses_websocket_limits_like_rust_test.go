package model

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/coder/websocket"
)

// TestResponsesWebsocketMaxMessageBytesLikeRust pins Rust #60841: only a
// positive integer header becomes the peer's request size limit.
func TestResponsesWebsocketMaxMessageBytesLikeRust(t *testing.T) {
	header := http.Header{}
	header.Set("x-codex-websocket-max-message-bytes", "4096")
	if got := responsesWebsocketMaxMessageBytes(header); got != 4096 {
		t.Fatalf("limit = %d, want 4096", got)
	}
	for _, value := range []string{"", "abc", "0", "-5"} {
		header.Set("x-codex-websocket-max-message-bytes", value)
		if got := responsesWebsocketMaxMessageBytes(header); got != 0 {
			t.Fatalf("limit for %q = %d, want 0", value, got)
		}
	}
	if got := responsesWebsocketMaxMessageBytes(nil); got != 0 {
		t.Fatalf("nil limit = %d, want 0", got)
	}
}

// TestWebsocketOversizedCloseLikeRust pins Rust #60840: only close code 1009
// means the peer rejected the request size.
func TestWebsocketOversizedCloseLikeRust(t *testing.T) {
	if !websocketOversizedClose(websocket.CloseError{Code: websocket.StatusMessageTooBig}) {
		t.Fatal("1009 close was not classified as oversized")
	}
	if websocketOversizedClose(websocket.CloseError{Code: websocket.StatusGoingAway}) {
		t.Fatal("going-away close was classified as oversized")
	}
	if !websocketOversizedClose(fmt.Errorf("read: %w", websocket.CloseError{Code: websocket.StatusMessageTooBig})) {
		t.Fatal("wrapped 1009 close was not classified as oversized")
	}
	if websocketOversizedClose(fmt.Errorf("plain")) {
		t.Fatal("plain error was classified as oversized")
	}
}
