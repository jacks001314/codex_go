package model

import (
	"errors"
	"net/http"
	"testing"

	"codex_go/codexapi"
)

// TestResponsesHTTPErrorDistinguishesHTTPQuotaErrorsFromRateLimits mirrors Rust
// #44492: HTTP 429 quota bodies map to the quota error kind while
// rate_limit_exceeded / slow_down keep the generic rate-limit error.
func TestResponsesHTTPErrorDistinguishesHTTPQuotaErrorsFromRateLimits(t *testing.T) {
	for _, body := range []string{
		`{"error":{"type":"insufficient_quota"}}`,
		`{"error":{"code":"insufficient_quota"}}`,
		`{"error":{"code":"credit_balance_exhausted"}}`,
		`{"error":{"code":"organization_spend_limit_exceeded"}}`,
		`{"error":{"code":"project_spend_limit_exceeded"}}`,
		`{"error":{"code":"organization_usage_limit_exceeded"}}`,
	} {
		err := responsesHTTPError("OpenAI", http.StatusTooManyRequests, http.Header{}, []byte(body))
		var apiErr *codexapi.APIError
		if !errors.As(err, &apiErr) {
			t.Fatalf("body %s: error type = %T, want *codexapi.APIError", body, err)
		}
		if apiErr.Kind != codexapi.ErrorQuotaExceeded || apiErr.Status != http.StatusTooManyRequests {
			t.Fatalf("body %s: error = %#v, want quota exceeded with status 429", body, apiErr)
		}
	}

	for _, body := range []string{
		`{"error":{"type":"rate_limit_error","code":"rate_limit_exceeded"}}`,
		`{"error":{"type":"rate_limit_error","code":"slow_down"}}`,
	} {
		err := responsesHTTPError("OpenAI", http.StatusTooManyRequests, http.Header{}, []byte(body))
		var apiErr *codexapi.APIError
		if errors.As(err, &apiErr) {
			t.Fatalf("body %s: error = %#v, want a plain ResponsesAPIError", body, apiErr)
		}
		var responsesErr *ResponsesAPIError
		if !errors.As(err, &responsesErr) || responsesErr.StatusCode != http.StatusTooManyRequests {
			t.Fatalf("body %s: error = %#v, want *ResponsesAPIError with status 429", body, err)
		}
	}
}
