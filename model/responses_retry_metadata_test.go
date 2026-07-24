package model

import (
	"errors"
	"net/http"
	"testing"
	"time"

	"codex_go/codexapi"
)

func TestResponsesHTTPErrorPreservesRetryAfterMetadata(t *testing.T) {
	err := responsesHTTPError("openai", http.StatusServiceUnavailable, http.Header{
		"Retry-After": []string{"17"},
	}, []byte(`{"error":{"message":"busy"}}`))

	var apiErr *ResponsesAPIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("responsesHTTPError() = %T, want *ResponsesAPIError", err)
	}
	if got := codexapi.RetryDelay(err); got != 17*time.Second {
		t.Fatalf("RetryDelay() = %v", got)
	}
}

func TestResponsesHTTPErrorPreservesExplicitZeroRetryAfter(t *testing.T) {
	err := responsesHTTPError("openai", http.StatusServiceUnavailable, http.Header{
		"Retry-After": []string{"0"},
	}, nil)
	if got, ok := codexapi.RetryDelayInfo(err); !ok || got != 0 {
		t.Fatalf("RetryDelayInfo() = %v, %t", got, ok)
	}
}
