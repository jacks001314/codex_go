package model

import (
	"errors"
	"net/http"
	"testing"
	"time"

	"codex_go/codexapi"
)

// TestAPISpendLimitScopesLikeRust mirrors Rust #62329: API key, project, and
// organization spend limits are distinct terminal errors across HTTP, SSE
// events, and failed responses, and keep no retry even with Retry-After.
func TestAPISpendLimitScopesLikeRust(t *testing.T) {
	cases := []struct {
		code    string
		kind    codexapi.APIErrorKind
		message string
	}{
		{"api_key_spend_limit_exceeded", codexapi.ErrorAPIKeySpendLimitExceeded, "Your API key has reached its spend limit."},
		{"project_spend_limit_exceeded", codexapi.ErrorProjectSpendLimitExceeded, "Your API project has reached its spend limit."},
		{"organization_spend_limit_exceeded", codexapi.ErrorOrganizationSpendLimitExceeded, "Your API organization has reached its spend limit."},
	}
	for _, tc := range cases {
		t.Run(tc.code+"/streamed", func(t *testing.T) {
			got := terminalProviderError(&responsesAgentAPIErrorBody{Code: tc.code, Message: "provider copy"}, 0)
			if got == nil || got.Kind != tc.kind || got.Message != tc.message {
				t.Fatalf("terminalProviderError(%q, 0) = %#v, want %s/%q", tc.code, got, tc.kind, tc.message)
			}
		})
		t.Run(tc.code+"/429", func(t *testing.T) {
			got := terminalProviderError(&responsesAgentAPIErrorBody{Code: tc.code}, http.StatusTooManyRequests)
			if got == nil || got.Kind != tc.kind || got.Status != http.StatusTooManyRequests {
				t.Fatalf("terminalProviderError(%q, 429) = %#v, want %s/429", tc.code, got, tc.kind)
			}
		})
		t.Run(tc.code+"/other status stays generic", func(t *testing.T) {
			if got := terminalProviderError(&responsesAgentAPIErrorBody{Code: tc.code}, http.StatusBadRequest); got != nil {
				t.Fatalf("terminalProviderError(%q, 400) = %#v, want nil", tc.code, got)
			}
		})
		t.Run(tc.code+"/response.failed", func(t *testing.T) {
			raw := `{"type":"response.failed","response":{"error":{"code":"` + tc.code + `","message":"provider copy"}}}`
			var apiErr *codexapi.APIError
			if err := responseFailedError([]byte(raw)); !errors.As(err, &apiErr) || apiErr.Kind != tc.kind {
				t.Fatalf("responseFailedError(%q) = %#v, want %s", tc.code, err, tc.kind)
			}
		})
		t.Run(tc.code+"/http 429", func(t *testing.T) {
			body := []byte(`{"error":{"code":"` + tc.code + `","message":"provider copy"}}`)
			var apiErr *codexapi.APIError
			if err := responsesHTTPError("openai", http.StatusTooManyRequests, http.Header{}, body); !errors.As(err, &apiErr) || apiErr.Kind != tc.kind {
				t.Fatalf("responsesHTTPError(%q) = %#v, want %s", tc.code, err, tc.kind)
			}
		})
		t.Run(tc.code+"/retry-after is not honored", func(t *testing.T) {
			retryAfter, ok := codexapi.NewRetryAfter(time.Second)
			if !ok {
				t.Fatal("failed to build retry-after")
			}
			err := (&codexapi.APIError{Kind: tc.kind, Status: http.StatusTooManyRequests}).WithRetryAfter(retryAfter)
			if isRetryableResponsesStreamError(err) {
				t.Fatalf("%s must stay terminal even with Retry-After", tc.code)
			}
		})
	}
}

func TestQuotaErrorCodesExcludeScopedSpendLimitsLikeRust(t *testing.T) {
	for _, code := range []string{"project_spend_limit_exceeded", "organization_spend_limit_exceeded"} {
		if responsesQuotaErrorCodes[code] {
			t.Fatalf("%s must not stay in the generic quota arm", code)
		}
	}
	if !responsesQuotaErrorCodes["insufficient_quota"] || !responsesQuotaErrorCodes["credit_balance_exhausted"] {
		t.Fatal("generic quota codes must stay in the quota arm")
	}
}
