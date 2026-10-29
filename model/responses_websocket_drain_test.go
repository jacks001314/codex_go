package model

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// Mirrors Rust #55969: a mailbox preemption interrupts a Responses Lite
// WebSocket stream with `response.interrupt`, drains to the terminal event and
// retains the completed items and the response ID for the follow-up request.
func TestResponsesAgentRunnerDrainsWebSocketOnMailboxPreemptionLikeRust(t *testing.T) {
	boundarySent := make(chan struct{})
	interrupts := make(chan map[string]any, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
		_ = conn.Write(r.Context(), websocket.MessageText, []byte(`{"type":"response.output_item.done","item":{"id":"boundary","type":"reasoning","summary":[{"type":"summary_text","text":"Preparing the next action"}]}}`))
		close(boundarySent)
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
		// A partial item that the server discards must not reach the client.
		_ = conn.Write(r.Context(), websocket.MessageText, []byte(`{"type":"response.output_text.delta","item_id":"note","delta":"discarded"}`))
		_ = conn.Write(r.Context(), websocket.MessageText, []byte(`{"type":"response.output_item.done","item":{"id":"note","type":"message","role":"assistant","content":[{"type":"output_text","text":"completed after interrupt"}]}}`))
		_ = conn.Write(r.Context(), websocket.MessageText, []byte(`{"type":"response.completed","response":{"id":"first","output":[{"id":"note","type":"message","role":"assistant","content":[{"type":"output_text","text":"completed after interrupt"}]}],"usage":{"input_tokens":5,"output_tokens":3,"total_tokens":8}}}`))
	}))
	defer server.Close()

	preempt := make(chan struct{})
	go func() {
		<-boundarySent
		close(preempt)
	}()
	streamed := []ResponsesStreamEventKind{}
	runner := NewResponsesAgentRunner(&ResponsesAgentOptions{
		Provider: &APIProvider{BaseURL: server.URL}, SupportsWebsockets: true, WebsocketConnectTimeout: time.Second,
	})
	response, err := runner.RunWebSocket(context.Background(), &AgentRequest{
		Model: "gpt-6-sol", Prompt: "plan the next action while the worker finishes", Preempt: preempt,
		StreamHandler: func(event *ResponsesStreamEvent) {
			if event != nil {
				streamed = append(streamed, event.Kind)
			}
		},
	})
	if err != nil {
		t.Fatalf("RunWebSocket() error = %v", err)
	}
	if response == nil || !response.Preempted || !response.Drained {
		t.Fatalf("response = %#v, want a drained preemption", response)
	}
	if response.ResponseID != "first" {
		t.Fatalf("response id = %q, want first", response.ResponseID)
	}
	itemIDs := []string{}
	for _, item := range response.Items {
		itemIDs = append(itemIDs, item.ID)
	}
	if len(itemIDs) != 2 || itemIDs[0] != "boundary" || itemIDs[1] != "note" {
		t.Fatalf("drained items = %v, want boundary and note", itemIDs)
	}
	if response.Message != "completed after interrupt" {
		t.Fatalf("message = %q, want the completed item text", response.Message)
	}
	for _, kind := range streamed {
		if kind == ResponsesStreamEventOutputText {
			t.Fatalf("a discarded partial delta reached the stream handler: %v", streamed)
		}
	}
	select {
	case interrupt := <-interrupts:
		if interrupt["type"] != "response.interrupt" || interrupt["response_id"] != "first" || interrupt["mode"] != "discard_partial_items" {
			t.Fatalf("interrupt = %#v", interrupt)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the client never sent response.interrupt")
	}
}

// The server may finish the response before it reads the interrupt, and the
// interrupted stream needs no acknowledgement: the drain still retains the
// buffered completion and the response ID (Rust #55969).
func TestResponsesAgentRunnerDrainsBufferedWebSocketCompletionWithoutAcknowledgement(t *testing.T) {
	interrupts := make(chan map[string]any, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
		_ = conn.Write(r.Context(), websocket.MessageText, []byte(`{"type":"response.output_item.done","item":{"id":"boundary","type":"reasoning","summary":[]}}`))
		_ = conn.Write(r.Context(), websocket.MessageText, []byte(`{"type":"response.completed","response":{"id":"first","output":[],"usage":{"input_tokens":2,"output_tokens":1,"total_tokens":3}}}`))
		_, data, err := conn.Read(r.Context())
		if err != nil {
			return
		}
		var interrupt map[string]any
		if json.Unmarshal(data, &interrupt) == nil {
			interrupts <- interrupt
		}
	}))
	defer server.Close()

	preempt := make(chan struct{})
	close(preempt)
	runner := NewResponsesAgentRunner(&ResponsesAgentOptions{
		Provider: &APIProvider{BaseURL: server.URL}, SupportsWebsockets: true, WebsocketConnectTimeout: time.Second,
	})
	response, err := runner.RunWebSocket(context.Background(), &AgentRequest{Model: "gpt-6-sol", Prompt: "plan", Preempt: preempt})
	if err != nil {
		t.Fatalf("RunWebSocket() error = %v", err)
	}
	if response == nil || !response.Preempted || !response.Drained || response.ResponseID != "first" {
		t.Fatalf("response = %#v, want a drained completion for first", response)
	}
	if len(response.Items) != 1 || response.Items[0].ID != "boundary" {
		t.Fatalf("drained items = %#v, want the buffered boundary", response.Items)
	}
	select {
	case <-interrupts:
	case <-time.After(2 * time.Second):
		t.Fatal("the client never sent response.interrupt")
	}
}

// A disconnect during the drain keeps the completed items and turns the step
// into a follow-up instead of an error, because the queued mail already
// requires another request (Rust #55969).
func TestResponsesAgentRunnerKeepsDrainedItemsWhenWebSocketClosesEarly(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Errorf("Accept() error = %v", err)
			return
		}
		if _, _, err := conn.Read(r.Context()); err != nil {
			t.Errorf("create read error = %v", err)
			return
		}
		_ = conn.Write(r.Context(), websocket.MessageText, []byte(`{"type":"response.created","response":{"id":"first"}}`))
		if _, _, err := conn.Read(r.Context()); err != nil {
			t.Errorf("interrupt read error = %v", err)
			return
		}
		// The completed item was already on the wire when the producer exited.
		_ = conn.Write(r.Context(), websocket.MessageText, []byte(`{"type":"response.output_item.done","item":{"id":"boundary","type":"reasoning","summary":[]}}`))
		_ = conn.Close(websocket.StatusInternalError, "producer exited")
	}))
	defer server.Close()

	preempt := make(chan struct{})
	close(preempt)
	runner := NewResponsesAgentRunner(&ResponsesAgentOptions{
		Provider: &APIProvider{BaseURL: server.URL}, SupportsWebsockets: true, WebsocketConnectTimeout: time.Second,
	})
	response, err := runner.RunWebSocket(context.Background(), &AgentRequest{Model: "gpt-6-sol", Prompt: "plan", Preempt: preempt})
	if err != nil {
		t.Fatalf("RunWebSocket() error = %v, want a drained follow-up", err)
	}
	if response == nil || !response.Preempted || !response.Drained || response.ResponseID != "first" {
		t.Fatalf("response = %#v, want a drained follow-up for first", response)
	}
	if len(response.Items) != 1 || response.Items[0].ID != "boundary" {
		t.Fatalf("drained items = %#v, want the completed boundary", response.Items)
	}
}

// Models without Responses Lite keep Rust #48141's behavior: the preemption
// drops the connection and returns no output, with no interrupt frame.
func TestResponsesAgentRunnerPreemptsNonLiteWebSocketWithoutInterrupt(t *testing.T) {
	interruptSeen := make(chan struct{}, 1)
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
		for {
			_, data, err := conn.Read(r.Context())
			if err != nil {
				return
			}
			var event map[string]any
			if json.Unmarshal(data, &event) == nil && event["type"] == "response.interrupt" {
				interruptSeen <- struct{}{}
				return
			}
		}
	}))
	defer server.Close()

	preempt := make(chan struct{})
	close(preempt)
	runner := NewResponsesAgentRunner(&ResponsesAgentOptions{
		Provider: &APIProvider{BaseURL: server.URL}, SupportsWebsockets: true, WebsocketConnectTimeout: time.Second,
	})
	response, err := runner.RunWebSocket(context.Background(), &AgentRequest{Model: "gpt-test", Prompt: "plan", Preempt: preempt})
	if err != nil {
		t.Fatalf("RunWebSocket() error = %v", err)
	}
	if response == nil || !response.Preempted || response.Drained || len(response.Items) != 0 {
		t.Fatalf("response = %#v, want an undrained preemption", response)
	}
	select {
	case <-interruptSeen:
		t.Fatal("a non-lite response must not receive response.interrupt")
	case <-time.After(200 * time.Millisecond):
	}
}
