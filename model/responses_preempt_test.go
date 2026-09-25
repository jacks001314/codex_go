package model

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func preemptTestRunner(serverURL string, stream bool, maxRetries uint64) *ResponsesAgentRunner {
	return NewResponsesAgentRunner(&ResponsesAgentOptions{
		Provider: &APIProvider{Name: OpenAIProviderName, BaseURL: serverURL + "/v1", RequestMaxRetries: maxRetries},
		Stream:   stream,
	})
}

// Mirrors Rust #48141: a step that was already signaled returns a preempted
// result (no assistant output) instead of starting the request.
func TestResponsesRunnerPreemptionBeforeRequestReturnsPreemptedResult(t *testing.T) {
	var requests int32
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		atomic.AddInt32(&requests, 1)
	}))
	defer server.Close()

	preempt := make(chan struct{})
	close(preempt)
	runner := preemptTestRunner(server.URL, false, 1)
	response, err := runner.Run(context.Background(), &AgentRequest{Prompt: "hello", Model: "gpt-test", Preempt: preempt})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if response == nil || !response.Preempted || len(response.Items) != 0 {
		t.Fatalf("response = %#v", response)
	}
	if got := atomic.LoadInt32(&requests); got != 0 {
		t.Fatalf("the provider was called %d times", got)
	}
}

// Mirrors Rust #48141: preemption happens while the response stream is in
// flight, so the request is abandoned instead of waiting for its completion.
func TestResponsesRunnerPreemptionInterruptsStreamingRequest(t *testing.T) {
	started := make(chan struct{})
	released := make(chan struct{})
	var once sync.Once
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "text/event-stream")
		writer.WriteHeader(http.StatusOK)
		if flusher, ok := writer.(http.Flusher); ok {
			flusher.Flush()
		}
		once.Do(func() { close(started) })
		select {
		case <-released:
		case <-request.Context().Done():
		}
	}))
	defer server.Close()
	defer close(released)

	runner := preemptTestRunner(server.URL, true, 1)
	preempt := make(chan struct{})
	type outcome struct {
		response *AgentResponse
		err      error
	}
	done := make(chan outcome, 1)
	go func() {
		response, err := runner.Run(context.Background(), &AgentRequest{Prompt: "hello", Model: "gpt-test", Preempt: preempt})
		done <- outcome{response: response, err: err}
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("the streaming request never started")
	}
	close(preempt)
	select {
	case result := <-done:
		if result.err != nil {
			t.Fatalf("Run() error = %v", result.err)
		}
		if result.response == nil || !result.response.Preempted {
			t.Fatalf("response = %#v", result.response)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the streaming request was not preempted")
	}
}

// Mirrors Rust #48141: the retry backoff is preemptible, so new user input does
// not wait for the remaining stream retry budget.
func TestResponsesRunnerPreemptionInterruptsRetryBackoff(t *testing.T) {
	var attempts int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		atomic.AddInt32(&attempts, 1)
		writer.WriteHeader(http.StatusInternalServerError)
		_, _ = writer.Write([]byte(`{"error":{"message":"boom"}}`))
	}))
	defer server.Close()

	runner := preemptTestRunner(server.URL, false, 5)
	preempt := make(chan struct{})
	go func() {
		time.Sleep(150 * time.Millisecond)
		close(preempt)
	}()
	begin := time.Now()
	response, err := runner.Run(context.Background(), &AgentRequest{Prompt: "hello", Model: "gpt-test", Preempt: preempt})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if response == nil || !response.Preempted {
		t.Fatalf("response = %#v", response)
	}
	if elapsed := time.Since(begin); elapsed > 5*time.Second {
		t.Fatalf("preemption did not interrupt the retry backoff: %v", elapsed)
	}
	if got := atomic.LoadInt32(&attempts); got > 2 {
		t.Fatalf("attempts = %d, want the preempted request to stop retrying", got)
	}
}

// Mirrors Rust #48141's ModelClientSession::drop_connection: a preempted request
// drops its cached connection and the continuation state, so the replacement
// request sends full history.
func TestResponsesRunnerPreemptionDropsConnectionAndContinuation(t *testing.T) {
	runner := preemptTestRunner("https://example.test", false, 1)
	request := &AgentRequest{ThreadID: "thread-a", TurnID: "turn-1"}
	session := runner.websocketSession(request)
	if session == nil {
		t.Fatal("no cached websocket session")
	}
	runner.turnState.mu.Lock()
	runner.turnState.turnID = "turn-1"
	runner.turnState.value = "continuation"
	runner.turnState.mu.Unlock()

	runner.dropConnectionForRequest(request)

	if got := runner.websocketSession(request); got == session {
		t.Fatal("the preempted request kept its cached connection")
	}
	runner.turnState.mu.Lock()
	turnID, value := runner.turnState.turnID, runner.turnState.value
	runner.turnState.mu.Unlock()
	if turnID != "" || value != "" {
		t.Fatalf("continuation state = (%q, %q), want it cleared", turnID, value)
	}
}
