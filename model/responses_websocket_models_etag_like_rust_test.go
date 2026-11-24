package model

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// recordingModelsETagManager records RefreshIfNewETag calls while delegating
// every other ModelsManager method to a static catalog.
type recordingModelsETagManager struct {
	*StaticModelsManager
	calls chan string
}

func (m *recordingModelsETagManager) RefreshIfNewETag(etag string) {
	if m == nil || m.calls == nil {
		return
	}
	select {
	case m.calls <- etag:
	default:
	}
}

// TestResponsesWebSocketHandshakeModelsETagLikeRust pins Rust #63533: the
// handshake x-models-etag refreshes the catalog once, at the first generation
// of the connection. Warmup leaves it pending and a reused socket never
// replays it.
func TestResponsesWebSocketHandshakeModelsETagLikeRust(t *testing.T) {
	var connects atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if !isResponsesWebSocketUpgrade(request) {
			http.Error(w, "unexpected HTTP request", http.StatusInternalServerError)
			return
		}
		connects.Add(1)
		w.Header().Set(responsesModelsETagHeader, `"models-1"`)
		conn, err := websocket.Accept(w, request, nil)
		if err != nil {
			t.Errorf("websocket.Accept() error = %v", err)
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "done")
		for index := 0; index < 3; index++ {
			if _, _, err := conn.Read(request.Context()); err != nil {
				return
			}
			id := fmt.Sprintf("resp-etag-%d", index+1)
			_ = conn.Write(request.Context(), websocket.MessageText, []byte(`{"type":"response.created","response":{"id":"`+id+`"}}`))
			_ = conn.Write(request.Context(), websocket.MessageText, []byte(`{"type":"response.output_item.done","item":{"id":"msg-etag","type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}}`))
			_ = conn.Write(request.Context(), websocket.MessageText, []byte(`{"type":"response.completed","response":{"id":"`+id+`","output":[{"id":"msg-etag","type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}}`))
		}
	}))
	defer server.Close()

	manager := &recordingModelsETagManager{StaticModelsManager: NewBundledModelsManager(), calls: make(chan string, 8)}
	runner := NewResponsesAgentRunner(&ResponsesAgentOptions{
		Provider:                &APIProvider{BaseURL: server.URL},
		Stream:                  true,
		SupportsWebsockets:      true,
		WebsocketConnectTimeout: time.Second,
		HTTPClient:              server.Client(),
		ModelsManager:           manager,
	})
	request := &AgentRequest{
		Prompt:            "hello",
		Model:             "gpt-test",
		ThreadID:          "thread-etag",
		TurnID:            "turn-etag",
		ParallelToolCalls: true,
	}
	if _, err := runner.Prewarm(context.Background(), request); err != nil {
		t.Fatalf("Prewarm() error = %v", err)
	}
	select {
	case etag := <-manager.calls:
		t.Fatalf("warmup refreshed the catalog with %q", etag)
	case <-time.After(100 * time.Millisecond):
	}

	if _, err := runner.Run(context.Background(), request); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	select {
	case etag := <-manager.calls:
		if etag != `"models-1"` {
			t.Fatalf("refreshed with %q, want the handshake ETag", etag)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the first generation did not refresh the catalog")
	}

	// A second step reuses the socket and must not replay the ETag.
	if _, err := runner.Run(context.Background(), request); err != nil {
		t.Fatalf("second Run() error = %v", err)
	}
	select {
	case etag := <-manager.calls:
		t.Fatalf("a reused socket replayed the ETag %q", etag)
	case <-time.After(200 * time.Millisecond):
	}
	if got := connects.Load(); got != 1 {
		t.Fatalf("websocket connections = %d, want 1", got)
	}
}
