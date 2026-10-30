package model

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// Rust #57864: cancelling a Responses Lite turn with `interrupt_on_cancel`
// enabled interrupts the upstream response, keeps the completed usage, and
// leaves the socket reusable for the next request.
func TestResponsesAgentRunnerInterruptsUpstreamOnCancelLikeRust(t *testing.T) {
	interrupts := make(chan map[string]any, 1)
	var connections atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		connections.Add(1)
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Errorf("Accept() error = %v", err)
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "done")
		if _, _, err := conn.Read(r.Context()); err != nil {
			t.Errorf("create read error = %v", err)
			return
		}
		_ = conn.Write(r.Context(), websocket.MessageText, []byte(`{"type":"response.created","response":{"id":"first"}}`))
		_ = conn.Write(r.Context(), websocket.MessageText, []byte(`{"type":"response.output_item.done","item":{"id":"note","type":"message","role":"assistant","content":[{"type":"output_text","text":"partial answer"}]}}`))
		_, data, err := conn.Read(r.Context())
		if err != nil {
			t.Errorf("interrupt read error = %v", err)
			return
		}
		var interrupt map[string]any
		if err := json.Unmarshal(data, &interrupt); err != nil {
			t.Errorf("interrupt decode error = %v", err)
			return
		}
		interrupts <- interrupt
		_ = conn.Write(r.Context(), websocket.MessageText, []byte(`{"type":"response.interrupt.accepted","response_id":"first"}`))
		_ = conn.Write(r.Context(), websocket.MessageText, []byte(`{"type":"response.completed","response":{"id":"first","output":[{"id":"note","type":"message","role":"assistant","content":[{"type":"output_text","text":"partial answer"}]}],"usage":{"input_tokens":5,"output_tokens":3,"total_tokens":8}}}`))

		// The same connection serves the follow-up request, proving the
		// cancelled response left the socket reusable.
		if _, _, err := conn.Read(r.Context()); err != nil {
			t.Errorf("second create read error = %v", err)
			return
		}
		_ = conn.Write(r.Context(), websocket.MessageText, []byte(`{"type":"response.created","response":{"id":"second"}}`))
		_ = conn.Write(r.Context(), websocket.MessageText, []byte(`{"type":"response.completed","response":{"id":"second","output":[{"id":"next","type":"message","role":"assistant","content":[{"type":"output_text","text":"next answer"}]}],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}}`))
	}))
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runner := NewResponsesAgentRunner(&ResponsesAgentOptions{
		Provider: &APIProvider{BaseURL: server.URL}, SupportsWebsockets: true, WebsocketConnectTimeout: time.Second,
	})
	response, err := runner.RunWebSocket(ctx, &AgentRequest{
		Model: "gpt-6-sol", Prompt: "answer this", InterruptOnCancel: true,
		StreamHandler: func(event *ResponsesStreamEvent) {
			// Cancel once the server has supplied the response id the interrupt
			// has to name.
			if event != nil && event.Kind == ResponsesStreamEventOutputDone {
				cancel()
			}
		},
	})
	if err != nil {
		t.Fatalf("RunWebSocket() error = %v", err)
	}
	if response == nil || !response.Canceled {
		t.Fatalf("response = %#v, want a cancelled response", response)
	}
	if response.ResponseID != "first" {
		t.Fatalf("response id = %q, want first", response.ResponseID)
	}
	if response.Usage.TotalTokens != 8 {
		t.Fatalf("usage = %#v, want the completed usage 8", response.Usage)
	}
	select {
	case interrupt := <-interrupts:
		if interrupt["type"] != "response.interrupt" || interrupt["response_id"] != "first" || interrupt["mode"] != "discard_partial_items" {
			t.Fatalf("interrupt = %#v", interrupt)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the client never sent response.interrupt on cancel")
	}
	second, err := runner.RunWebSocket(context.Background(), &AgentRequest{Model: "gpt-6-sol", Prompt: "next"})
	if err != nil {
		t.Fatalf("second RunWebSocket() error = %v", err)
	}
	if second == nil || second.ResponseID != "second" {
		t.Fatalf("second response = %#v", second)
	}
	if got := connections.Load(); got != 1 {
		t.Fatalf("connections = %d, want the cancelled response to keep its socket", got)
	}
}

// Without the feature flag a cancelled sampling request still abandons the
// stream immediately, so no interrupt reaches the server.
func TestResponsesAgentRunnerCancelWithoutFeatureDoesNotInterruptLikeRust(t *testing.T) {
	interrupts := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Errorf("Accept() error = %v", err)
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "done")
		if _, _, err := conn.Read(r.Context()); err != nil {
			return
		}
		_ = conn.Write(r.Context(), websocket.MessageText, []byte(`{"type":"response.created","response":{"id":"first"}}`))
		if _, _, err := conn.Read(r.Context()); err == nil {
			interrupts <- struct{}{}
		}
	}))
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runner := NewResponsesAgentRunner(&ResponsesAgentOptions{
		Provider: &APIProvider{BaseURL: server.URL}, SupportsWebsockets: true, WebsocketConnectTimeout: time.Second,
	})
	_, err := runner.RunWebSocket(ctx, &AgentRequest{
		Model: "gpt-6-sol", Prompt: "answer this",
		StreamHandler: func(event *ResponsesStreamEvent) {
			if event != nil {
				cancel()
			}
		},
	})
	if err == nil {
		t.Fatal("cancelled sampling should fail without interrupt_on_cancel")
	}
	select {
	case <-interrupts:
		t.Fatal("without interrupt_on_cancel the client must not send response.interrupt")
	case <-time.After(200 * time.Millisecond):
	}
}
