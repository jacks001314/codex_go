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

// Rust parses the 429 body once as `UsageErrorResponse`, so a body whose
// declared fields are malformed also skips the usage-not-included and quota
// classifications and falls through to the generic retry error - not only the
// usage-limit branch. The `flex_unavailable` code is checked on the lenient
// error object before that strict decode, so it still wins.
func TestResponsesHTTPErrorGatesSibling429BranchesOnTheStrictDecodeLikeRust(t *testing.T) {
	for _, body := range []string{
		`{"error":{"type":"usage_not_included","resets_at":"soon"}}`,
		`{"error":{"type":"usage_not_included","plan_type":42}}`,
		`{"error":{"code":"insufficient_quota","resets_at":"soon"}}`,
		`{"error":{"code":"credit_balance_exhausted","plan_type":42}}`,
	} {
		err := responsesHTTPError("OpenAI", http.StatusTooManyRequests, http.Header{}, []byte(body))
		var apiErr *codexapi.APIError
		if errors.As(err, &apiErr) {
			t.Fatalf("body %s was classified as %#v, want the generic retry classification", body, apiErr)
		}
		if _, ok := err.(*ResponsesAPIError); !ok {
			t.Fatalf("body %s error = %#v, want *ResponsesAPIError", body, err)
		}
	}

	// A decodable body with an extra declared field still classifies.
	quota := responsesHTTPError("OpenAI", http.StatusTooManyRequests, http.Header{},
		[]byte(`{"error":{"code":"insufficient_quota","resets_at":1700000000}}`))
	var apiErr *codexapi.APIError
	if !errors.As(quota, &apiErr) || apiErr.Kind != codexapi.ErrorQuotaExceeded {
		t.Fatalf("quota 429 = %#v, want quotaExceeded", quota)
	}

	// `flex_unavailable` is checked before the strict decode.
	flex := responsesHTTPError("OpenAI", http.StatusTooManyRequests, http.Header{},
		[]byte(`{"error":{"code":"flex_unavailable","type":"usage_limit_reached","resets_at":"soon"}}`))
	if !errors.As(flex, &apiErr) || apiErr.Kind != codexapi.ErrorFlexUnavailable {
		t.Fatalf("flex 429 = %#v, want flexUnavailable", flex)
	}
}
