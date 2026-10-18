package execserver

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// Mirrors Rust #52234: the exec-server echoes a supplied X-Request-ID on the
// WebSocket upgrade response and leaves it absent otherwise.
func TestExecServerEchoesWebSocketRequestIDLikeRust(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writer := http.ResponseWriter(w)
		if requestID := strings.TrimSpace(r.Header.Get("X-Request-ID")); requestID != "" {
			writer = &websocketRequestIDWriter{ResponseWriter: w, requestID: requestID}
		}
		conn, err := websocket.Accept(writer, r, nil)
		if err != nil {
			return
		}
		_ = conn.Close(websocket.StatusNormalClosure, "")
	}))
	defer server.Close()
	url := "ws" + strings.TrimPrefix(server.URL, "http")
	for _, testCase := range []struct {
		name   string
		header http.Header
		want   string
	}{
		{name: "supplied", header: http.Header{"X-Request-ID": []string{"caller-id"}}, want: "caller-id"},
		{name: "absent", header: nil, want: ""},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			conn, response, err := websocket.Dial(ctx, url, &websocket.DialOptions{HTTPHeader: testCase.header})
			if err != nil {
				t.Fatalf("Dial() error = %v", err)
			}
			if got := response.Header.Get("X-Request-ID"); got != testCase.want {
				_ = conn.Close(websocket.StatusNormalClosure, "")
				t.Fatalf("X-Request-ID = %q, want %q", got, testCase.want)
			}
			_ = conn.Close(websocket.StatusNormalClosure, "")
		})
	}
}
