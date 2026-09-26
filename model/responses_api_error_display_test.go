package model

import (
	"net/http"
	"strings"
	"testing"
)

// TestResponsesAPIErrorDisplayMatchesRust mirrors Rust's `UnexpectedResponseError`
// display suite (`protocol/src/error_tests.rs`): the app-server reports this
// string as the turn error message, so the status phrasing, the body preference,
// the ellipsis truncation and the response-context suffixes must match Rust
// rather than a Go-only prefix.
func TestResponsesAPIErrorDisplayMatchesRust(t *testing.T) {
	cases := []struct {
		name string
		err  *ResponsesAPIError
		want string
	}{
		{
			name: "non-html body with url",
			err: &ResponsesAPIError{
				StatusCode: http.StatusForbidden,
				Body:       "plain text error",
				URL:        "http://example.com/plain",
			},
			want: "unexpected status 403 Forbidden: plain text error, url: http://example.com/plain",
		},
		{
			name: "user message preserves response context",
			err: &ResponsesAPIError{
				StatusCode:  http.StatusUnauthorized,
				Body:        "provider-specific response",
				UserMessage: "Provider-specific guidance",
				URL:         "https://example.com/v1/responses",
				RequestID:   "req-provider",
			},
			want: "Provider-specific guidance, url: https://example.com/v1/responses, request id: req-provider",
		},
		{
			name: "json error message preferred",
			err: &ResponsesAPIError{
				StatusCode: http.StatusUnauthorized,
				Body:       `{"error":{"message":"Workspace is not authorized in this region."},"status":401}`,
				URL:        "https://chatgpt.com/backend-api/codex/responses",
				RequestID:  "req-123",
			},
			want: "unexpected status 401 Unauthorized: Workspace is not authorized in this region., url: https://chatgpt.com/backend-api/codex/responses, request id: req-123",
		},
		{
			name: "long body truncated with ellipsis",
			err: &ResponsesAPIError{
				StatusCode: http.StatusBadGateway,
				Body:       strings.Repeat("x", unexpectedResponseBodyMaxBytes+10),
				URL:        "http://example.com/long",
				RequestID:  "req-long",
			},
			want: "unexpected status 502 Bad Gateway: " + strings.Repeat("x", unexpectedResponseBodyMaxBytes) + "..., url: http://example.com/long, request id: req-long",
		},
		{
			name: "cf-ray and request id",
			err: &ResponsesAPIError{
				StatusCode: http.StatusUnauthorized,
				Body:       "plain text error",
				URL:        "https://chatgpt.com/backend-api/codex/responses",
				CFRay:      "9c81f9f18f2fa49d-LHR",
				RequestID:  "req-xyz",
			},
			want: "unexpected status 401 Unauthorized: plain text error, url: https://chatgpt.com/backend-api/codex/responses, cf-ray: 9c81f9f18f2fa49d-LHR, request id: req-xyz",
		},
		{
			name: "identity auth details",
			err: &ResponsesAPIError{
				StatusCode:             http.StatusUnauthorized,
				Body:                   "plain text error",
				URL:                    "https://chatgpt.com/backend-api/codex/models",
				CFRay:                  "cf-ray-auth-401-test",
				RequestID:              "req-auth",
				AuthorizationError:     "missing_authorization_header",
				AuthorizationErrorCode: "token_expired",
			},
			want: "unexpected status 401 Unauthorized: plain text error, url: https://chatgpt.com/backend-api/codex/models, cf-ray: cf-ray-auth-401-test, request id: req-auth, auth error: missing_authorization_header, auth error code: token_expired",
		},
		{
			name: "empty body reports unknown error",
			err:  &ResponsesAPIError{StatusCode: http.StatusBadGateway},
			want: "unexpected status 502 Bad Gateway: Unknown error",
		},
		{
			name: "unnamed status omits the reason phrase",
			err:  &ResponsesAPIError{StatusCode: 599, Body: "boom"},
			want: "unexpected status 599: boom",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := testCase.err.Error(); got != testCase.want {
				t.Fatalf("Error() = %q, want %q", got, testCase.want)
			}
		})
	}
}

// TestResponsesProviderUserMessageMatchesRust pins the two provider-specific
// user_message overrides: the Cloudflare-blocked 403 copy from codex-api and
// Amazon Bedrock's expired-signature 401 copy from model-provider.
func TestResponsesProviderUserMessageMatchesRust(t *testing.T) {
	cloudflare := responsesHTTPError("openai", http.StatusForbidden, http.Header{},
		[]byte("Access blocked by Cloudflare. This request was blocked."))
	cloudflareErr, ok := cloudflare.(*ResponsesAPIError)
	if !ok {
		t.Fatalf("cloudflare error type = %T", cloudflare)
	}
	want := cloudflareBlockedMessage + " (status 403 Forbidden)"
	if cloudflareErr.UserMessage != want {
		t.Fatalf("cloudflare user message = %q, want %q", cloudflareErr.UserMessage, want)
	}
	if got := cloudflareErr.Error(); !strings.HasPrefix(got, want) {
		t.Fatalf("cloudflare error = %q, want the user message prefix", got)
	}

	// A 403 without both Cloudflare markers keeps the status rendering.
	plain := responsesHTTPError("openai", http.StatusForbidden, http.Header{}, []byte("blocked"))
	plainErr, ok := plain.(*ResponsesAPIError)
	if !ok {
		t.Fatalf("plain error type = %T", plain)
	}
	if plainErr.UserMessage != "" {
		t.Fatalf("plain user message = %q, want empty", plainErr.UserMessage)
	}
	if got := plainErr.Error(); got != "unexpected status 403 Forbidden: blocked" {
		t.Fatalf("plain error = %q", got)
	}

	bedrock := responsesHTTPError(AmazonBedrockProviderName, http.StatusUnauthorized, http.Header{},
		[]byte("Signature expired: 20260609T133205Z is now earlier than 20260614T062525Z"))
	bedrockErr, ok := bedrock.(*ResponsesAPIError)
	if !ok {
		t.Fatalf("bedrock error type = %T", bedrock)
	}
	if bedrockErr.UserMessage != bedrockExpiredSignatureMessage {
		t.Fatalf("bedrock user message = %q", bedrockErr.UserMessage)
	}
}
