package model

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// websocketPoolServer counts handshakes and answers every request on the socket
// it was sent on, keeping the connection open so a reuse is observable.
type websocketPoolServer struct {
	t          *testing.T
	mu         sync.Mutex
	handshakes int
	requests   int
	headers    []http.Header
}

func (s *websocketPoolServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		s.t.Errorf("websocket.Accept() error = %v", err)
		return
	}
	defer conn.Close(websocket.StatusNormalClosure, "done")
	s.mu.Lock()
	s.handshakes++
	connection := s.handshakes
	s.headers = append(s.headers, r.Header.Clone())
	s.mu.Unlock()
	for {
		_, data, err := conn.Read(r.Context())
		if err != nil {
			return
		}
		var payload map[string]any
		_ = json.Unmarshal(data, &payload)
		s.mu.Lock()
		s.requests++
		index := s.requests
		s.mu.Unlock()
		body := fmt.Sprintf(
			`{"type":"response.completed","response":{"id":"pool-%d","output":[{"id":"m%d","type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}},"connection":%d}`,
			index, index, connection)
		_ = conn.Write(r.Context(), websocket.MessageText, []byte(body))
	}
}

func (s *websocketPoolServer) snapshot() (int, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.handshakes, s.requests
}

func newPoolRunner(t *testing.T, server *httptest.Server, runtimeAuth bool) *ResponsesAgentRunner {
	t.Helper()
	return NewResponsesAgentRunner(&ResponsesAgentOptions{
		Provider:                &APIProvider{BaseURL: server.URL},
		Stream:                  true,
		SupportsWebsockets:      true,
		WebsocketConnectTimeout: time.Second,
		RuntimeAuthActive:       runtimeAuth,
	})
}

// TestWebsocketPoolReusesConnectionWhenActorUnchangedLikeRust mirrors the idle
// reuse branch of `codex-rs/ext/guardian-v2/src/async_scorer/sampler/connection_pool.rs`:
// the same key, runtime identity and auth owner with an unchanged credential
// reuses the pooled connection instead of opening another handshake.
func TestWebsocketPoolReusesConnectionWhenActorUnchangedLikeRust(t *testing.T) {
	pool := &websocketPoolServer{t: t}
	server := httptest.NewServer(pool)
	defer server.Close()

	runner := newPoolRunner(t, server, false)
	request := &AgentRequest{Prompt: "hello", Model: "gpt-test", ThreadID: "thread-pool-reuse"}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	for attempt := 0; attempt < 2; attempt++ {
		response, err := runner.Run(ctx, request)
		if err != nil {
			t.Fatalf("Run() attempt %d error = %v", attempt, err)
		}
		if response == nil {
			t.Fatalf("Run() attempt %d returned no response", attempt)
		}
	}
	handshakes, requests := pool.snapshot()
	if handshakes != 1 {
		t.Fatalf("handshakes = %d, want 1: an unchanged actor must reuse the connection", handshakes)
	}
	if requests != 2 {
		t.Fatalf("requests = %d, want 2", requests)
	}
}

// TestRuntimeWebsocketConnectionNeverJoinsIdlePoolLikeRust mirrors
// `connection_pool.rs` (`runtime_id.is_some()` -> `fresh_connection`,
// `ConnectionLease::reuse` keeps only non-runtime connections) and
// `client_websockets_runtime_auth.rs` (two requests on one runtime credential
// still produce two handshakes).
func TestRuntimeWebsocketConnectionNeverJoinsIdlePoolLikeRust(t *testing.T) {
	pool := &websocketPoolServer{t: t}
	server := httptest.NewServer(pool)
	defer server.Close()

	runner := newPoolRunner(t, server, true)
	request := &AgentRequest{Prompt: "hello", Model: "gpt-test", ThreadID: "thread-pool-runtime"}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	for attempt := 0; attempt < 2; attempt++ {
		response, err := runner.Run(ctx, request)
		if err != nil {
			t.Fatalf("Run() attempt %d error = %v", attempt, err)
		}
		if response == nil {
			t.Fatalf("Run() attempt %d returned no response", attempt)
		}
	}
	handshakes, requests := pool.snapshot()
	if handshakes != 2 {
		t.Fatalf("handshakes = %d, want 2: a runtime connection must not join the idle pool", handshakes)
	}
	if requests != 2 {
		t.Fatalf("requests = %d, want 2", requests)
	}
}
