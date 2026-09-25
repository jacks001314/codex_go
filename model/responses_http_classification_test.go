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

// TestResponsesHTTPErrorPreservesUsageLimitWindowLikeRust mirrors Rust #48174's
// api_bridge mapping: a 429 whose error type is `usage_limit_reached` becomes a
// usage-limit failure that carries the server-selected window when the response
// supplied an unsigned integer that fits in u16. Missing, null, malformed and
// out-of-range values stay unknown.
func TestResponsesHTTPErrorPreservesUsageLimitWindowLikeRust(t *testing.T) {
	for _, testCase := range []struct {
		name string
		body string
		want *uint16
	}{
		{name: "five hour", body: `{"error":{"type":"usage_limit_reached","message":"usage limit reached","plan_type":"pro","limit_window_minutes":300}}`, want: uint16PtrModel(300)},
		{name: "weekly", body: `{"error":{"type":"usage_limit_reached","message":"usage limit reached","limit_window_minutes":10080}}`, want: uint16PtrModel(10080)},
		{name: "missing", body: `{"error":{"type":"usage_limit_reached","message":"usage limit reached"}}`, want: nil},
		{name: "null", body: `{"error":{"type":"usage_limit_reached","message":"usage limit reached","limit_window_minutes":null}}`, want: nil},
		{name: "malformed", body: `{"error":{"type":"usage_limit_reached","message":"usage limit reached","limit_window_minutes":"five hours"}}`, want: nil},
		{name: "out of range", body: `{"error":{"type":"usage_limit_reached","message":"usage limit reached","limit_window_minutes":70000}}`, want: nil},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			err := responsesHTTPError("OpenAI", http.StatusTooManyRequests, http.Header{}, []byte(testCase.body))
			var apiErr *codexapi.APIError
			if !errors.As(err, &apiErr) || apiErr.Kind != codexapi.ErrorRateLimit {
				t.Fatalf("usage limit error = %#v, want a rate-limit error", err)
			}
			if apiErr.Status != http.StatusTooManyRequests {
				t.Fatalf("status = %d, want 429", apiErr.Status)
			}
			got := apiErr.UsageLimitWindowMinutes
			if (got == nil) != (testCase.want == nil) || (got != nil && *got != *testCase.want) {
				t.Fatalf("window = %v, want %v", got, testCase.want)
			}
			if details := apiErr.Details(); !sameWindow(details.UsageLimitWindowMinutes, testCase.want) {
				t.Fatalf("details window = %v, want %v", details.UsageLimitWindowMinutes, testCase.want)
			}
		})
	}

	// Another 429 kind keeps its own classification and carries no window.
	quota := responsesHTTPError("OpenAI", http.StatusTooManyRequests, http.Header{},
		[]byte(`{"error":{"type":"insufficient_quota","message":"limit reached","limit_window_minutes":300}}`))
	var apiErr *codexapi.APIError
	if !errors.As(quota, &apiErr) || apiErr.Kind != codexapi.ErrorQuotaExceeded {
		t.Fatalf("quota 429 = %#v, want quotaExceeded", quota)
	}
	if apiErr.UsageLimitWindowMinutes != nil {
		t.Fatalf("quota window = %v, want unknown", apiErr.UsageLimitWindowMinutes)
	}
}

func uint16PtrModel(value uint16) *uint16 { return &value }

func sameWindow(got *uint16, want *uint16) bool {
	if got == nil || want == nil {
		return got == nil && want == nil
	}
	return *got == *want
}

// TestWebsocketUsageLimitErrorPreservesWindowLikeRust mirrors Rust #48174's
// wrapped-WebSocket mapping: a usage-limit error event must carry a numeric
// status, and its window survives to the classified error.
func TestWebsocketUsageLimitErrorPreservesWindowLikeRust(t *testing.T) {
	event := map[string]any{
		"type":   "error",
		"status": float64(http.StatusTooManyRequests),
		"error": map[string]any{
			"type":                 "usage_limit_reached",
			"message":              "The usage limit has been reached",
			"plan_type":            "pro",
			"limit_window_minutes": float64(10080),
		},
	}
	apiErr, ok := websocketUsageLimitError(event)
	if !ok {
		t.Fatal("usage-limit websocket event must map to a classified error")
	}
	if apiErr.Kind != codexapi.ErrorRateLimit || apiErr.Status != http.StatusTooManyRequests {
		t.Fatalf("websocket usage limit = %#v", apiErr)
	}
	if apiErr.UsageLimitWindowMinutes == nil || *apiErr.UsageLimitWindowMinutes != 10080 {
		t.Fatalf("websocket window = %v, want 10080", apiErr.UsageLimitWindowMinutes)
	}

	// Without a status the wrapped error is not mapped (Rust's
	// `parse_wrapped_websocket_error_event_without_status_is_not_mapped`).
	if _, ok := websocketUsageLimitError(map[string]any{
		"type":  "error",
		"error": map[string]any{"type": "usage_limit_reached", "message": "usage limit reached"},
	}); ok {
		t.Fatal("a status-less usage-limit event must not be mapped")
	}
}
