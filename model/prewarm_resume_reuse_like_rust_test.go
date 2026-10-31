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

// These tests mirror Rust #47709: a resume warmup routes through the cached
// WebSocket session, skips preparation while that socket is warm, and repairs
// the socket once the peer closed it.

type prewarmResumeServer struct {
	t          *testing.T
	mu         sync.Mutex
	payloads   []map[string]any
	handshakes int
	closeFirst chan struct{}
}

func (s *prewarmResumeServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		s.t.Errorf("Accept() error = %v", err)
		return
	}
	defer conn.Close(websocket.StatusNormalClosure, "done")
	s.mu.Lock()
	s.handshakes++
	s.mu.Unlock()
	for {
		_, data, err := conn.Read(r.Context())
		if err != nil {
			return
		}
		var payload map[string]any
		if unmarshalErr := json.Unmarshal(data, &payload); unmarshalErr != nil {
			s.t.Errorf("Unmarshal() error = %v", unmarshalErr)
			return
		}
		s.mu.Lock()
		s.payloads = append(s.payloads, payload)
		connection := s.handshakes
		s.mu.Unlock()
		responseID := fmt.Sprintf("warm-%d", connection)
		_ = conn.Write(r.Context(), websocket.MessageText, []byte(fmt.Sprintf(`{"type":"response.completed","response":{"id":%q}}`, responseID)))
		if connection == 1 {
			<-s.closeFirst
			return
		}
	}
}

func (s *prewarmResumeServer) snapshot() (int, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.handshakes, len(s.payloads)
}

func (s *prewarmResumeServer) payload(index int) map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	if index >= len(s.payloads) {
		return nil
	}
	return s.payloads[index]
}

func waitForPrewarmCondition(t *testing.T, timeout time.Duration, condition func() bool, message string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		if condition() {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s", message)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestResponsesAgentRunnerResumePrewarmSkipsWarmAndRepairsClosedSocketLikeRust(t *testing.T) {
	server := &prewarmResumeServer{t: t, closeFirst: make(chan struct{})}
	httpServer := httptest.NewServer(server)
	defer httpServer.Close()

	runner := NewResponsesAgentRunner(&ResponsesAgentOptions{
		Provider:                &APIProvider{BaseURL: httpServer.URL},
		SupportsWebsockets:      true,
		WebsocketConnectTimeout: time.Second,
	})
	request := &AgentRequest{Model: "gpt-test", ThreadID: "thread-resume-prewarm", Instructions: "be brief"}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	response, err := runner.Prewarm(ctx, request)
	if err != nil || response == nil || response.ResponseID != "warm-1" {
		t.Fatalf("first prewarm response=%#v err=%v", response, err)
	}

	// A healthy warm resume skips preparation and sends nothing on the socket.
	skipped, err := runner.Prewarm(ctx, request)
	if err != nil || skipped != nil {
		t.Fatalf("warm resume prewarm=%#v err=%v", skipped, err)
	}
	if handshakes, requests := server.snapshot(); handshakes != 1 || requests != 1 {
		t.Fatalf("warm resume handshakes=%d requests=%d, want 1/1", handshakes, requests)
	}

	// Once the peer closed the warm socket, the next resume prepares and redials
	// instead of trusting the completed warmup.
	close(server.closeFirst)
	waitForPrewarmCondition(t, 5*time.Second, func() bool {
		session := runner.websocketSession(request)
		session.mu.Lock()
		defer session.mu.Unlock()
		return !websocketSessionPrewarmed(session)
	}, "cached websocket was not observed closed")

	repaired, err := runner.Prewarm(ctx, request)
	if err != nil || repaired == nil || repaired.ResponseID != "warm-2" {
		t.Fatalf("repaired prewarm response=%#v err=%v", repaired, err)
	}
	if handshakes, requests := server.snapshot(); handshakes != 2 || requests != 2 {
		t.Fatalf("repaired handshakes=%d requests=%d, want 2/2", handshakes, requests)
	}
	payload := server.payload(1)
	if payload["generate"] != false {
		t.Fatalf("resume warmup generate = %#v, want false", payload["generate"])
	}
	if _, ok := payload["previous_response_id"]; ok {
		t.Fatalf("resume warmup carried previous_response_id: %#v", payload)
	}
	items, _ := payload["input"].([]any)
	if len(items) == 0 {
		t.Fatalf("resume warmup input = %#v", payload["input"])
	}
}
