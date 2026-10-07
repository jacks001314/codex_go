package model

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"codex_go/codexapi"
)

// Rust #49441 (6ba4bf9e64) makes `CodexErr::retry_delay` answer
// `server_retry_delay()` for `ServerOverloaded` and `RetryLimit`, so those two
// classes retry only while the server supplies retry advice, within the
// configured budgets, and the HTTP mapping keeps the `Retry-After` deadline it
// received. These tests mirror codex-rs/core/tests/suite/retry_after.rs
// `responses_http_overload_respects_retry_limits`, `responses_http_429_uses_retry_after`
// and codex-rs/codex-api/src/api_bridge_tests.rs
// `http_retry_deadline_survives_mapping_and_respects_terminal_errors`.

const (
	retryAdviceOverloadBody  = `{"error":{"code":"server_is_overloaded","message":"Selected model is at capacity."}}`
	retryAdviceRateLimitBody = `{"error":{"code":"rate_limit_exceeded","message":"Rate limit reached."}}`
)

// retryAdviceRecoveredSSE is the successful second response the Rust 429 test
// serves after the advised retry.
func retryAdviceRecoveredSSE() string {
	return responsesSSE(
		`{"type":"response.output_item.done","item":{"id":"msg-1","type":"message","role":"assistant","content":[{"type":"output_text","text":"recovered"}]}}`,
		`{"type":"response.completed","response":{"id":"recovered"}}`,
	)
}

type retryAdviceStep struct {
	status     int
	body       string
	retryAfter string
}

// runRetryAdviceScenario serves `steps` in order (repeating the last one) and
// returns the request count and the runner's error.
func runRetryAdviceScenario(t *testing.T, steps []retryAdviceStep, requestMaxRetries, streamMaxRetries uint64) (int, error) {
	t.Helper()
	var (
		mu    sync.Mutex
		count int
	)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_, _ = io.ReadAll(request.Body)
		mu.Lock()
		index := count
		count++
		mu.Unlock()
		if index >= len(steps) {
			index = len(steps) - 1
		}
		step := steps[index]
		if step.retryAfter != "" {
			writer.Header().Set("Retry-After", step.retryAfter)
		}
		if step.status == http.StatusOK {
			writer.Header().Set("Content-Type", "text/event-stream")
			_, _ = writer.Write([]byte(step.body))
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(step.status)
		_, _ = writer.Write([]byte(step.body))
	}))
	defer server.Close()
	runner := NewResponsesAgentRunner(&ResponsesAgentOptions{
		Provider: &APIProvider{
			Name:              OpenAIProviderName,
			BaseURL:           server.URL + "/v1",
			RequestMaxRetries: requestMaxRetries,
			StreamMaxRetries:  streamMaxRetries,
		},
		Stream: true,
	})
	_, err := runner.Run(context.Background(), &AgentRequest{Prompt: "hi", Model: "gpt-5.5"})
	mu.Lock()
	defer mu.Unlock()
	return count, err
}

// Mirrors the Rust retry-limit table: advice changes delays but never extends
// either retry budget or its request count.
func TestResponsesStreamOverloadRetryRespectsAdviceLikeRust(t *testing.T) {
	cases := []struct {
		name              string
		retryAfter        string
		requestMaxRetries uint64
		streamMaxRetries  uint64
		wantRequests      int
	}{
		{name: "without advice keeps only the request budget", retryAfter: "", requestMaxRetries: 2, streamMaxRetries: 2, wantRequests: 3},
		{name: "advice keeps the stream budget", retryAfter: "0", requestMaxRetries: 2, streamMaxRetries: 2, wantRequests: 9},
		{name: "advice without stream retries", retryAfter: "0", requestMaxRetries: 2, streamMaxRetries: 0, wantRequests: 3},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			steps := []retryAdviceStep{{status: http.StatusServiceUnavailable, body: retryAdviceOverloadBody, retryAfter: testCase.retryAfter}}
			requests, err := runRetryAdviceScenario(t, steps, testCase.requestMaxRetries, testCase.streamMaxRetries)
			if requests != testCase.wantRequests {
				t.Fatalf("requests = %d, want %d", requests, testCase.wantRequests)
			}
			if err == nil {
				t.Fatal("Run() error = nil, want the overload failure")
			}
			if err.Error() != "Selected model is at capacity. Please try a different model." {
				t.Fatalf("error = %q", err.Error())
			}
		})
	}
}

// Mirrors `responses_http_429_uses_retry_after`: a 429 with server advice retries
// in the sampling loop and completes without a terminal error.
func TestResponsesStream429RetryUsesAdviceLikeRust(t *testing.T) {
	t.Run("advised 429 recovers in the sampling loop", func(t *testing.T) {
		steps := []retryAdviceStep{
			{status: http.StatusTooManyRequests, body: retryAdviceRateLimitBody, retryAfter: "0"},
			{status: http.StatusOK, body: retryAdviceRecoveredSSE()},
		}
		requests, err := runRetryAdviceScenario(t, steps, 0, 1)
		if err != nil {
			t.Fatalf("Run() error = %v, want the advised 429 to recover", err)
		}
		if requests != 2 {
			t.Fatalf("requests = %d, want 2", requests)
		}
	})

	t.Run("headerless 429 stays terminal", func(t *testing.T) {
		steps := []retryAdviceStep{{status: http.StatusTooManyRequests, body: retryAdviceRateLimitBody}}
		requests, err := runRetryAdviceScenario(t, steps, 0, 1)
		if err == nil {
			t.Fatal("Run() error = nil, want the headerless 429 failure")
		}
		if requests != 1 {
			t.Fatalf("requests = %d, want 1", requests)
		}
	})
}

// Mirrors `http_retry_deadline_survives_mapping_and_respects_terminal_errors`:
// the mapping keeps the elapsed advice for the retryable classes, and quota,
// usage-limit and policy failures stay terminal even with advice.
func TestResponsesHTTPOverloadKeepsRetryAdviceLikeRust(t *testing.T) {
	headers := http.Header{}
	headers.Set("Retry-After", "6")
	for _, testCase := range []struct {
		status     int
		code       string
		retryable  bool
		wantAdvice bool
	}{
		{status: http.StatusServiceUnavailable, code: "server_is_overloaded", retryable: true, wantAdvice: true},
		{status: http.StatusTooManyRequests, code: "rate_limit_exceeded", retryable: true, wantAdvice: true},
		{status: http.StatusTooManyRequests, code: "insufficient_quota", retryable: false},
		{status: http.StatusTooManyRequests, code: "usage_limit_reached", retryable: false},
		{status: http.StatusBadRequest, code: "cyber_policy", retryable: false},
	} {
		t.Run(testCase.code, func(t *testing.T) {
			body := []byte(`{"error":{"type":"` + testCase.code + `","code":"` + testCase.code + `","message":"detail"}}`)
			err := responsesHTTPError(OpenAIProviderName, testCase.status, headers, body)
			if got := isRetryableResponsesStreamError(err); got != testCase.retryable {
				t.Fatalf("isRetryableResponsesStreamError(%s) = %t, want %t", testCase.code, got, testCase.retryable)
			}
			delay, advised := codexapi.RetryDelayInfo(err)
			if advised != testCase.wantAdvice {
				t.Fatalf("%s: retry advice present = %t, want %t", testCase.code, advised, testCase.wantAdvice)
			}
			if advised && delay != 6*time.Second {
				t.Fatalf("%s: retry advice = %s, want 6s", testCase.code, delay)
			}
		})
	}
}
