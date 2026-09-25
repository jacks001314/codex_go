package model

import (
	"errors"
	"net/http"
	"testing"

	"codex_go/codexapi"
)

// TestResponsesHTTPErrorDistinguishesCapacityFromSlowDownLikeRust mirrors Rust
// #45602's api_bridge classification: a 503 body with `server_is_overloaded`
// stays a terminal overload, while `slow_down` becomes a retryable rate limit
// with the server message, and anything else stays a generic response error.
func TestResponsesHTTPErrorDistinguishesCapacityFromSlowDownLikeRust(t *testing.T) {
	overloaded := responsesHTTPError("OpenAI", http.StatusServiceUnavailable, http.Header{},
		[]byte(`{"error":{"code":"server_is_overloaded","message":"Selected model is at capacity."}}`))
	var apiErr *codexapi.APIError
	if !errors.As(overloaded, &apiErr) || apiErr.Kind != codexapi.ErrorServerOverloaded {
		t.Fatalf("overloaded error = %#v, want serverOverloaded", overloaded)
	}
	if apiErr.Message != "Selected model is at capacity." {
		t.Fatalf("overloaded message = %q", apiErr.Message)
	}

	slowDown := responsesHTTPError("OpenAI", http.StatusServiceUnavailable, http.Header{},
		[]byte(`{"error":{"code":"slow_down","message":"retry later"}}`))
	if !errors.As(slowDown, &apiErr) || apiErr.Kind != codexapi.ErrorRateLimitExceeded {
		t.Fatalf("slow_down error = %#v, want rateLimitExceeded", slowDown)
	}
	if apiErr.Message != "retry later" {
		t.Fatalf("slow_down message = %q, want the server message", apiErr.Message)
	}
	if !isRetryableResponsesStreamError(slowDown) {
		t.Fatal("slow_down should be retryable")
	}

	// An unrecognized code keeps the generic 503 response error (Rust's Other
	// classification) instead of being folded into the overload kind.
	unknown := responsesHTTPError("OpenAI", http.StatusServiceUnavailable, http.Header{},
		[]byte(`{"error":{"code":"unknown_error","message":"retry later"}}`))
	if errors.As(unknown, &apiErr) {
		t.Fatalf("unknown_error classified as %#v, want a plain ResponsesAPIError", apiErr)
	}
	var responsesErr *ResponsesAPIError
	if !errors.As(unknown, &responsesErr) || responsesErr.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("unknown_error error = %#v, want *ResponsesAPIError with status 503", unknown)
	}
}

// TestResponsesHTTPErrorRecognizesFlexUnavailableLikeRust mirrors Rust #47967:
// an HTTP 429 whose body carries `flex_unavailable` is a terminal Flex-capacity
// failure, not a retryable rate limit or a usage-limit failure.
func TestResponsesHTTPErrorRecognizesFlexUnavailableLikeRust(t *testing.T) {
	flex := responsesHTTPError("OpenAI", http.StatusTooManyRequests, http.Header{},
		[]byte(`{"error":{"code":"flex_unavailable","message":"Flex capacity unavailable."}}`))
	var apiErr *codexapi.APIError
	if !errors.As(flex, &apiErr) || apiErr.Kind != codexapi.ErrorFlexUnavailable {
		t.Fatalf("flex error = %#v, want flexUnavailable", flex)
	}
	if apiErr.Status != http.StatusTooManyRequests {
		t.Fatalf("flex status = %d, want 429", apiErr.Status)
	}
	if apiErr.Error() != "Flex capacity unavailable." {
		t.Fatalf("flex display = %q", apiErr.Error())
	}
	if isRetryableResponsesStreamError(flex) {
		t.Fatal("flex_unavailable must not be retryable")
	}

	// A 429 without the Flex code keeps the existing quota classification.
	quota := responsesHTTPError("OpenAI", http.StatusTooManyRequests, http.Header{},
		[]byte(`{"error":{"code":"credit_balance_exhausted","message":"limit reached"}}`))
	if !errors.As(quota, &apiErr) || apiErr.Kind != codexapi.ErrorQuotaExceeded {
		t.Fatalf("quota 429 = %#v, want quotaExceeded", quota)
	}
}
