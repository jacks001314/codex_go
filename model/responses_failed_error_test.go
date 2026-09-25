package model

import (
	"errors"
	"testing"

	"codex_go/codexapi"
)

// Mirrors Rust #48229's `malformed_failed_responses_use_stream_error`: a
// `response.failed` payload that cannot be decoded with the error object's
// declared types degrades to the stream-error sentinel, including when only a
// field this classification does not read (such as `plan_type`) is malformed.
func TestResponseFailedErrorRejectsMalformedPayloadsLikeRust(t *testing.T) {
	for _, raw := range []string{
		`{"type":"response.failed"}`,
		`{"type":"response.failed","response":null}`,
		`{"type":"response.failed","response":[]}`,
		`{"type":"response.failed","response":{}}`,
		`{"type":"response.failed","response":{"error":null}}`,
		`{"type":"response.failed","response":{"error":42}}`,
		`{"type":"response.failed","response":{"error":{"code":42}}}`,
		`{"type":"response.failed","response":{"error":{"code":"server_is_overloaded","plan_type":42}}}`,
		`{"type":"response.failed","response":{"error":{"code":"context_length_exceeded","resets_at":"soon"}}}`,
		`{"type":"response.failed","response":{"error":{"code":"bio_policy","message":42}}}`,
		`{"type":"response.failed","response":{"error":{"code":"invalid_prompt","type":false}}}`,
	} {
		if err := responseFailedError([]byte(raw)); !errors.Is(err, errResponsesStreamFailed) {
			t.Fatalf("responseFailedError(%s) = %#v, want the stream-error fallback", raw, err)
		}
	}
}

// The Flex shape is recognized before the strict decode (Rust's
// `parse_flex_unavailable` runs first), so an otherwise malformed payload still
// ends the turn without retries.
func TestResponseFailedErrorKeepsFlexUnavailableAheadOfStrictDecodingLikeRust(t *testing.T) {
	err := responseFailedError([]byte(`{"type":"response.failed","response":{"error":{"code":"flex_unavailable","plan_type":42,"message":"Flex capacity unavailable."}}}`))
	var apiErr *codexapi.APIError
	if !errors.As(err, &apiErr) || apiErr.Kind != codexapi.ErrorFlexUnavailable {
		t.Fatalf("flex error = %#v, want flex_unavailable", err)
	}
	if apiErr.Message != "Flex capacity unavailable." {
		t.Fatalf("flex message = %q", apiErr.Message)
	}
}

// Rust's `cyber_policy_message` and `invalid_prompt` fallbacks: a missing or
// blank cyber message becomes the cybersecurity notice, and a missing prompt
// message becomes "Invalid request." (a present blank message is kept).
func TestResponseFailedErrorPolicyFallbackMessagesLikeRust(t *testing.T) {
	cyber := responseFailedError([]byte(`{"type":"response.failed","response":{"error":{"code":"cyber_policy"}}}`))
	var apiErr *codexapi.APIError
	if !errors.As(cyber, &apiErr) || apiErr.Kind != codexapi.ErrorCyberPolicy || apiErr.Message != CyberPolicyFallbackMessage {
		t.Fatalf("cyber_policy fallback = %#v", cyber)
	}
	blankCyber := responseFailedError([]byte(`{"type":"response.failed","response":{"error":{"code":"cyber_policy","message":"   "}}}`))
	if !errors.As(blankCyber, &apiErr) || apiErr.Message != CyberPolicyFallbackMessage {
		t.Fatalf("blank cyber_policy fallback = %#v", blankCyber)
	}
	invalid := responseFailedError([]byte(`{"type":"response.failed","response":{"error":{"code":"invalid_prompt"}}}`))
	if !errors.As(invalid, &apiErr) || apiErr.Kind != codexapi.ErrorInvalidRequest || apiErr.Message != "Invalid request." {
		t.Fatalf("invalid_prompt fallback = %#v", invalid)
	}
	blankInvalid := responseFailedError([]byte(`{"type":"response.failed","response":{"error":{"code":"invalid_prompt","message":""}}}`))
	if !errors.As(blankInvalid, &apiErr) || apiErr.Message != "" {
		t.Fatalf("blank invalid_prompt message = %#v", blankInvalid)
	}
}
