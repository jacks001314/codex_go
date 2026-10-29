package model

import (
	"errors"
	"net/http"
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
	if !errors.As(invalid, &apiErr) || apiErr.Kind != codexapi.ErrorInvalidPrompt || apiErr.Message != InvalidPromptFallbackMessage {
		t.Fatalf("invalid_prompt fallback = %#v", invalid)
	}
	blankInvalid := responseFailedError([]byte(`{"type":"response.failed","response":{"error":{"code":"invalid_prompt","message":""}}}`))
	if !errors.As(blankInvalid, &apiErr) || apiErr.Kind != codexapi.ErrorInvalidPrompt || apiErr.Message != "" {
		t.Fatalf("blank invalid_prompt message = %#v", blankInvalid)
	}
	spacedInvalid := responseFailedError([]byte(`{"type":"response.failed","response":{"error":{"code":"invalid_prompt","message":"  "}}}`))
	if !errors.As(spacedInvalid, &apiErr) || apiErr.Kind != codexapi.ErrorInvalidPrompt || apiErr.Message != "  " {
		t.Fatalf("spaced invalid_prompt message = %#v", spacedInvalid)
	}
}

// OpenAI-compatible inference servers often report an oversized prompt as a
// plain 400 body without a Responses error code. The HTTP client must still
// surface it as a context-window failure so the turn can be compacted.
func TestResponsesHTTPErrorClassifiesProviderContextOverflow(t *testing.T) {
	for _, body := range []string{
		`{"error":{"message":"This model's maximum context length is 1048576 tokens. However, you requested 1299476 tokens (1299476 in the messages, 0 in the completion). Please reduce the length"}}`,
		`{"error":{"message":"Your input exceeds the context window of this model. Please adjust your input and try again."}}`,
		`{"error":{"message":"The request exceeds the available context size"}}`,
		`{"error":{"message":"prompt is too long"}}`,
		`{"error":{"code":"context_length_exceeded","message":"too big"}}`,
	} {
		err := responsesHTTPError("deepseek", http.StatusBadRequest, http.Header{}, []byte(body))
		var apiErr *codexapi.APIError
		if !errors.As(err, &apiErr) || apiErr.Kind != codexapi.ErrorContextWindowExceeded || apiErr.Status != http.StatusBadRequest {
			t.Fatalf("context overflow body %s => %#v, want context-window API error", body, err)
		}
	}

	err := responsesHTTPError("deepseek", http.StatusBadRequest, http.Header{}, []byte(`{"error":{"message":"invalid model"}}`))
	var apiErr *codexapi.APIError
	if errors.As(err, &apiErr) {
		t.Fatalf("unrelated 400 was classified as an API error: %#v", err)
	}
	var responsesErr *ResponsesAPIError
	if !errors.As(err, &responsesErr) || responsesErr.StatusCode != http.StatusBadRequest {
		t.Fatalf("unrelated 400 = %#v, want a generic Responses API error", err)
	}
}
