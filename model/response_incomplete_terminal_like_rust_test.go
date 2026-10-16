package model

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

// Rust #53451 ("Treat incomplete responses as terminal errors") makes an
// incomplete response - `max_output_tokens` and friends - a terminal
// `ResponseIncomplete` error: the stream stops immediately, keeps no retry
// budget, and compaction must not fall back to the selected model. The upstream
// regression is client.rs::incomplete_response_is_terminal, which runs the same
// incomplete sequence with `stream_max_retries = 2` and asserts that the mock
// sees exactly one request and that the turn ends with the error message.
//
// Go's carrier is the typed `ResponseIncompleteError` the responses stream
// returns, so this pins the same three properties: the typed class, the exact
// Rust message, and a single stream request despite an available retry budget.
func TestResponseIncompleteIsTerminalLikeRust(t *testing.T) {
	var requests int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&requests, 1)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(responsesSSE(
			`{"type":"response.created","response":{"id":"resp-incomplete"}}`,
			`{"type":"response.output_item.done","output_index":0,"item":{"type":"message","id":"msg-1","role":"assistant","content":[{"type":"output_text","text":"Partial reply."}]}}`,
			`{"type":"response.incomplete","response":{"id":"resp-incomplete","incomplete_details":{"reason":"max_output_tokens"}}}`,
		)))
	}))
	defer server.Close()

	runner := NewResponsesAgentRunner(&ResponsesAgentOptions{
		Provider: &APIProvider{BaseURL: server.URL + "/v1", StreamMaxRetries: 2},
		Stream:   true,
	})
	_, err := runner.Run(context.Background(), &AgentRequest{Prompt: "trigger output limit", Model: "gpt-test"})
	if err == nil {
		t.Fatal("Run() error = nil, want the terminal incomplete response")
	}
	if !IsResponseIncompleteError(err) {
		t.Fatalf("Run() error = %#v, want a ResponseIncompleteError (#53451)", err)
	}
	if got := err.Error(); got != "Incomplete response returned, reason: max_output_tokens" {
		t.Fatalf("Run() error message = %q", got)
	}
	if isRetryableResponsesStreamError(err) {
		t.Fatal("an incomplete response must not be retryable (#53451)")
	}
	if got := atomic.LoadInt32(&requests); got != 1 {
		t.Fatalf("stream requests = %d, want 1: the retry budget must not be spent on an incomplete response", got)
	}
}

// The content-filter reason keeps its own retryable classification (Rust
// #49119), so the new terminal class must not swallow it.
func TestContentFilterReasonKeepsItsRetryableClassLikeRust(t *testing.T) {
	err := responseIncompleteError([]byte(`{"type":"response.incomplete","response":{"id":"resp-1","incomplete_details":{"reason":"content_filter"}}}`))
	if IsResponseIncompleteError(err) {
		t.Fatalf("content filter error = %#v, want the retryable content-filter class", err)
	}
	if !isContentFilterStreamError(err) || !isRetryableResponsesStreamError(err) {
		t.Fatalf("content filter error = %#v, want retryable", err)
	}

	// The incomplete class is reported as its own sampler failure kind (Rust
	// #53451 records it separately from the generic stream failure).
	incomplete := responseIncompleteError([]byte(`{"type":"response.incomplete","response":{"id":"resp-1","incomplete_details":{"reason":"max_output_tokens"}}}`))
	if got := responsesDiagnosticErrorKind(incomplete); got != "response_incomplete" {
		t.Fatalf("diagnostic kind = %q, want response_incomplete", got)
	}
}
