package model

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync/atomic"
	"testing"

	"github.com/coder/websocket"
)

// Rust #53670 ("Account for token usage from incomplete responses") makes the
// terminal incomplete-response error carry the response id and the usage the
// server reported on that event:
// `protocol/src/error.rs::ResponseIncompleteError { reason, response_id,
// token_usage }`, filled by
// `codex-api/src/sse/responses.rs::process_responses_event` from
// `response.get("id").and_then(Value::as_str)` and
// `serde_json::from_value::<ResponseCompletedUsage>(usage.clone()).ok()`. A
// usage that is missing or malformed yields `None` - Rust never estimates one
// from the partial response. Upstream pins both transports with the same table
// (`client.rs::incomplete_response_is_terminal` over {sse, websocket} x
// {reported, missing, malformed} usage).
//
// Go reads both transports through the one carrier this table exercises: the
// SSE parser calls `responseIncompleteError` directly, and `runWebSocket`
// (model/responses_agent.go) feeds each WebSocket message to the same
// `responsesStreamAccumulator.apply` that calls it here.
func TestResponseIncompleteCarriesResponseIDAndReportedUsageLikeRust(t *testing.T) {
	cases := []struct {
		name      string
		event     string
		wantID    string
		wantUsage *AgentUsage
	}{
		{
			name:      "reported_usage",
			event:     `{"type":"response.incomplete","response":{"id":"resp-incomplete","incomplete_details":{"reason":"max_output_tokens"},"usage":{"input_tokens":120,"output_tokens":30,"total_tokens":150}}}`,
			wantID:    "resp-incomplete",
			wantUsage: &AgentUsage{InputTokens: 120, OutputTokens: 30, TotalTokens: 150},
		},
		{
			name:      "usage_with_details_and_budget_units",
			event:     `{"type":"response.incomplete","response":{"id":"resp-incomplete","incomplete_details":{"reason":"max_output_tokens"},"usage":{"input_tokens":120,"output_tokens":30,"total_tokens":150,"input_tokens_details":{"cached_tokens":90,"cache_write_tokens":8},"output_tokens_details":{"reasoning_tokens":12},"codex_rollout_budget_units":2.5}}}`,
			wantID:    "resp-incomplete",
			wantUsage: &AgentUsage{InputTokens: 120, OutputTokens: 30, TotalTokens: 150, CachedInputTokens: 90, CacheWriteInputTokens: 8, ReasoningOutputTokens: 12, CodexRolloutBudgetUnits: json.Number("2.5")},
		},
		{
			// Rust copies the reported usage verbatim, so a reported zero total
			// stays zero instead of being estimated from the token counts.
			name:      "reported_zero_total_is_not_estimated",
			event:     `{"type":"response.incomplete","response":{"id":"resp-incomplete","incomplete_details":{"reason":"max_output_tokens"},"usage":{"input_tokens":120,"output_tokens":30,"total_tokens":0}}}`,
			wantID:    "resp-incomplete",
			wantUsage: &AgentUsage{InputTokens: 120, OutputTokens: 30, TotalTokens: 0},
		},
		{
			name:   "missing_usage",
			event:  `{"type":"response.incomplete","response":{"id":"resp-incomplete","incomplete_details":{"reason":"max_output_tokens"}}}`,
			wantID: "resp-incomplete",
		},
		{
			name:   "null_usage",
			event:  `{"type":"response.incomplete","response":{"id":"resp-incomplete","incomplete_details":{"reason":"max_output_tokens"},"usage":null}}`,
			wantID: "resp-incomplete",
		},
		{
			name:   "malformed_usage",
			event:  `{"type":"response.incomplete","response":{"id":"resp-incomplete","incomplete_details":{"reason":"max_output_tokens"},"usage":{"malformed":true}}}`,
			wantID: "resp-incomplete",
		},
		{
			name:   "partial_usage_missing_required_member",
			event:  `{"type":"response.incomplete","response":{"id":"resp-incomplete","incomplete_details":{"reason":"max_output_tokens"},"usage":{"input_tokens":120,"output_tokens":30}}}`,
			wantID: "resp-incomplete",
		},
		{
			name:   "wrongly_typed_usage",
			event:  `{"type":"response.incomplete","response":{"id":"resp-incomplete","incomplete_details":{"reason":"max_output_tokens"},"usage":{"input_tokens":"120","output_tokens":30,"total_tokens":150}}}`,
			wantID: "resp-incomplete",
		},
		{
			name:   "malformed_usage_details",
			event:  `{"type":"response.incomplete","response":{"id":"resp-incomplete","incomplete_details":{"reason":"max_output_tokens"},"usage":{"input_tokens":120,"output_tokens":30,"total_tokens":150,"input_tokens_details":{}}}}`,
			wantID: "resp-incomplete",
		},
		{
			name:      "event_without_response_id",
			event:     `{"type":"response.incomplete","response":{"incomplete_details":{"reason":"max_output_tokens"},"usage":{"input_tokens":1,"output_tokens":2,"total_tokens":3}}}`,
			wantUsage: &AgentUsage{InputTokens: 1, OutputTokens: 2, TotalTokens: 3},
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			incomplete := requireIncompleteResponseError(t, responseIncompleteError([]byte(testCase.event)))
			if incomplete.ResponseID != testCase.wantID {
				t.Fatalf("ResponseID = %q, want %q", incomplete.ResponseID, testCase.wantID)
			}
			if !reflect.DeepEqual(incomplete.Usage, testCase.wantUsage) {
				t.Fatalf("Usage = %#v, want %#v", incomplete.Usage, testCase.wantUsage)
			}
		})
	}
}

// The same three usage arms end to end over the streamed transport: the
// terminal error keeps the reason and message Rust renders, does not spend the
// available stream retry budget, and carries only a usage the server actually
// reported.
func TestIncompleteResponseIsTerminalAndCarriesUsageLikeRust(t *testing.T) {
	cases := []struct {
		name      string
		usage     string
		wantUsage *AgentUsage
	}{
		{
			name:      "reported_usage",
			usage:     `,"usage":{"input_tokens":120,"output_tokens":30,"total_tokens":150}`,
			wantUsage: &AgentUsage{InputTokens: 120, OutputTokens: 30, TotalTokens: 150},
		},
		{name: "missing_usage"},
		{name: "malformed_usage", usage: `,"usage":{"malformed":true}`},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			requests := int32(0)
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				atomic.AddInt32(&requests, 1)
				writer.Header().Set("Content-Type", "text/event-stream")
				_, _ = writer.Write([]byte(responsesSSE(
					`{"type":"response.created","response":{"id":"resp-incomplete"}}`,
					`{"type":"response.incomplete","response":{"id":"resp-incomplete","incomplete_details":{"reason":"max_output_tokens"}`+testCase.usage+`}}`,
				)))
			}))
			defer server.Close()

			runner := NewResponsesAgentRunner(&ResponsesAgentOptions{
				Provider: &APIProvider{BaseURL: server.URL + "/v1", StreamMaxRetries: 2},
				Stream:   true,
			})
			_, err := runner.Run(context.Background(), &AgentRequest{Prompt: "trigger output limit", Model: "gpt-test"})
			incomplete := requireIncompleteResponseError(t, err)
			if got := incomplete.Error(); got != "Incomplete response returned, reason: max_output_tokens" {
				t.Fatalf("error message = %q", got)
			}
			if incomplete.ResponseID != "resp-incomplete" {
				t.Fatalf("ResponseID = %q, want resp-incomplete", incomplete.ResponseID)
			}
			if !reflect.DeepEqual(incomplete.Usage, testCase.wantUsage) {
				t.Fatalf("Usage = %#v, want %#v", incomplete.Usage, testCase.wantUsage)
			}
			if got := atomic.LoadInt32(&requests); got != 1 {
				t.Fatalf("stream requests = %d, want 1: the retry budget must not be spent on an incomplete response", got)
			}
		})
	}
}

// The websocket transport reads its events through the same accumulator entry
// point (Rust's `process_responses_event` serves both transports), so the
// carrier must hold there too - including for a malformed usage, which stays
// absent instead of becoming a zero-valued estimate.
func TestWebsocketIncompleteResponseCarriesReportedUsageLikeRust(t *testing.T) {
	cases := []struct {
		name      string
		usage     string
		wantUsage *AgentUsage
	}{
		{
			name:      "websocket_reported_usage",
			usage:     `,"usage":{"input_tokens":120,"output_tokens":30,"total_tokens":150}`,
			wantUsage: &AgentUsage{InputTokens: 120, OutputTokens: 30, TotalTokens: 150},
		},
		{name: "websocket_missing_usage"},
		{name: "websocket_malformed_usage", usage: `,"usage":{"malformed":true}`},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			incomplete := fmt.Sprintf(`{"type":"response.incomplete","response":{"id":"resp-ws-incomplete","incomplete_details":{"reason":"max_output_tokens"}%s}}`, testCase.usage)
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				conn, acceptErr := websocket.Accept(writer, request, nil)
				if acceptErr != nil {
					t.Errorf("websocket.Accept() error = %v", acceptErr)
					return
				}
				defer conn.Close(websocket.StatusNormalClosure, "done")
				_, _, _ = conn.Read(request.Context())
				_ = conn.Write(request.Context(), websocket.MessageText, []byte(`{"type":"response.created","response":{"id":"resp-ws-incomplete"}}`))
				_ = conn.Write(request.Context(), websocket.MessageText, []byte(incomplete))
			}))
			defer server.Close()

			runner := NewResponsesAgentRunner(&ResponsesAgentOptions{
				Provider:           &APIProvider{BaseURL: server.URL},
				SupportsWebsockets: true,
			})
			_, err := runner.RunWebSocket(context.Background(), &AgentRequest{Model: "gpt-test", Prompt: "trigger output limit"})
			incompleteErr := requireIncompleteResponseError(t, err)
			if incompleteErr.ResponseID != "resp-ws-incomplete" {
				t.Fatalf("ResponseID = %q, want resp-ws-incomplete", incompleteErr.ResponseID)
			}
			if !reflect.DeepEqual(incompleteErr.Usage, testCase.wantUsage) {
				t.Fatalf("Usage = %#v, want %#v", incompleteErr.Usage, testCase.wantUsage)
			}
		})
	}
}

func requireIncompleteResponseError(t *testing.T, err error) ResponseIncompleteError {
	t.Helper()
	if err == nil {
		t.Fatal("error = nil, want the terminal incomplete response")
	}
	var incomplete ResponseIncompleteError
	if !errors.As(err, &incomplete) {
		t.Fatalf("error = %#v, want a ResponseIncompleteError (#53670 carrier)", err)
	}
	return incomplete
}
