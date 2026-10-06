package model

import (
	"encoding/json"
	"testing"
	"time"

	"codex_go/codexapi"
)

// Mirrors Rust #50418 `rate_limit_plaintext_loses_to_valid_error_header`: a
// streamed `response.failed` error's `Retry-After` header wins over the
// rate-limit message advice, and an absent or invalid header falls back to it.
func TestResponseFailedPrefersRetryAfterHeaderLikeRust(t *testing.T) {
	cases := []struct {
		header    string
		hasHeader bool
		wantDelay time.Duration
	}{
		{header: "2", hasHeader: true, wantDelay: 2 * time.Second},
		{header: "0", hasHeader: true, wantDelay: 0},
		{header: "Wed, 21 Oct 2015 07:28:00 GMT", hasHeader: true, wantDelay: 0},
		{header: "bad", hasHeader: true, wantDelay: 35 * time.Second},
		{hasHeader: false, wantDelay: 35 * time.Second},
	}
	for _, code := range []string{"rate_limit_exceeded", "slow_down"} {
		for _, testCase := range cases {
			errorBody := map[string]any{
				"code":    code,
				"message": "Try again in 35 seconds.",
			}
			if testCase.hasHeader {
				errorBody["headers"] = map[string]any{"Retry-After": testCase.header}
			}
			raw, err := json.Marshal(map[string]any{
				"type":     "response.failed",
				"response": map[string]any{"error": errorBody},
			})
			if err != nil {
				t.Fatal(err)
			}
			delay, ok := codexapi.RetryDelayInfo(responseFailedError(raw))
			if !ok {
				t.Fatalf("code=%s header=%q: no retry delay reported", code, testCase.header)
			}
			if delay != testCase.wantDelay {
				t.Fatalf("code=%s header=%q: delay=%s, want %s", code, testCase.header, delay, testCase.wantDelay)
			}
		}
	}
}

// Mirrors Rust #50418: overload and generic retryable failures keep the
// server-provided header delay, and a malformed error object with a valid
// header still reports retryable advice instead of the plain stream sentinel.
func TestResponseFailedRetryAfterHeaderPreservedForRetryableErrorsLikeRust(t *testing.T) {
	for _, code := range []string{"server_is_overloaded", "some_new_code"} {
		raw, err := json.Marshal(map[string]any{
			"type": "response.failed",
			"response": map[string]any{"error": map[string]any{
				"code":    code,
				"message": "capacity",
				"headers": map[string]any{"retry-after": "7"},
			}},
		})
		if err != nil {
			t.Fatal(err)
		}
		delay, ok := codexapi.RetryDelayInfo(responseFailedError(raw))
		if !ok || delay != 7*time.Second {
			t.Fatalf("code=%s: delay=%s ok=%t, want 7s", code, delay, ok)
		}
	}

	malformed := []byte(`{"type":"response.failed","response":{"error":{"message":{"unexpected":true},"headers":{"Retry-After":"4"}}}}`)
	err := responseFailedError(malformed)
	var apiErr *codexapi.APIError
	if !asAPIError(err, &apiErr) || apiErr.Details().Kind != codexapi.ErrorRetryable {
		t.Fatalf("malformed error = %#v, want retryable", err)
	}
	if delay, ok := codexapi.RetryDelayInfo(err); !ok || delay != 4*time.Second {
		t.Fatalf("malformed delay = %s ok=%t, want 4s", delay, ok)
	}
}

// Mirrors Rust #50418 `flex_failure_with_retry_header_ends_stream_immediately`:
// a Flex failure stays terminal and ignores any retry header.
func TestResponseFailedFlexIgnoresRetryAfterHeaderLikeRust(t *testing.T) {
	raw := []byte(`{"type":"response.failed","response":{"error":{"code":"flex_unavailable","message":"Flex capacity unavailable.","headers":{"retry-after":"300"}}}}`)
	err := responseFailedError(raw)
	var apiErr *codexapi.APIError
	if !asAPIError(err, &apiErr) || apiErr.Details().Kind != codexapi.ErrorFlexUnavailable {
		t.Fatalf("flex error = %#v, want flex_unavailable", err)
	}
	if delay, ok := codexapi.RetryDelayInfo(err); ok {
		t.Fatalf("flex retry delay = %s, want none", delay)
	}
}

// Mirrors Rust #50418: an all-digit Retry-After value is a delay in seconds,
// while a past HTTP date yields zero rather than falling back to the message.
func TestResponsesRetryAfterFromHeaderValueLikeRust(t *testing.T) {
	// `now` is after the sample HTTP date, so that header yields zero.
	now := time.Date(2015, 10, 21, 8, 0, 0, 0, time.UTC)
	for _, testCase := range []struct {
		value     string
		wantDelay time.Duration
		wantOK    bool
	}{
		{value: "", wantOK: false},
		{value: "   ", wantOK: false},
		{value: "bad", wantOK: false},
		{value: "2.5", wantOK: false},
		{value: "12", wantDelay: 12 * time.Second, wantOK: true},
		{value: "0", wantDelay: 0, wantOK: true},
		{value: "Wed, 21 Oct 2015 07:28:00 GMT", wantDelay: 0, wantOK: true},
	} {
		delay, ok := responsesRetryAfterFromHeaderValue(testCase.value, now)
		if ok != testCase.wantOK {
			t.Fatalf("value=%q: ok=%t, want %t", testCase.value, ok, testCase.wantOK)
		}
		if ok && delay != testCase.wantDelay {
			t.Fatalf("value=%q: delay=%s, want %s", testCase.value, delay, testCase.wantDelay)
		}
	}
	// A future HTTP date is measured from the supplied instant.
	earlier := time.Date(2015, 10, 21, 7, 0, 0, 0, time.UTC)
	if delay, ok := responsesRetryAfterFromHeaderValue("Wed, 21 Oct 2015 07:28:00 GMT", earlier); !ok || delay != 28*time.Minute {
		t.Fatalf("future date delay = %s ok=%t, want 28m", delay, ok)
	}
}

func asAPIError(err error, target **codexapi.APIError) bool {
	apiErr, ok := err.(*codexapi.APIError)
	if ok {
		*target = apiErr
	}
	return ok
}
