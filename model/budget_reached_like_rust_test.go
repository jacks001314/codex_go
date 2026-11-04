package model

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"codex_go/codexapi"
)

// Rust #59228: `budget_reached` is a terminal session budget error for HTTP
// 400 responses and statusless streaming failures.
func TestResponsesBudgetReachedIsTerminalSessionBudgetErrorLikeRust(t *testing.T) {
	httpErr := responsesHTTPError("OpenAI", http.StatusBadRequest, http.Header{},
		[]byte(`{"error":{"code":"budget_reached","message":"out of budget"}}`))
	var apiErr *codexapi.APIError
	if !errors.As(httpErr, &apiErr) || apiErr.Kind != codexapi.ErrorSessionBudgetExceeded {
		t.Fatalf("budget_reached error = %#v, want sessionBudgetExceeded", httpErr)
	}
	if apiErr.Status != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", apiErr.Status)
	}
	if got := apiErr.Error(); got != "budget reached" {
		t.Fatalf("display = %q, want budget reached", got)
	}
	if isRetryableResponsesStreamError(httpErr) {
		t.Fatal("budget_reached must not be retried")
	}

	streamed, ok := terminalProviderStreamError([]byte(`{"type":"error","error":{"code":"budget_reached","message":"out of budget"},"headers":{"retry-after":"10"}}`))
	if !ok || streamed == nil || streamed.Kind != codexapi.ErrorSessionBudgetExceeded {
		t.Fatalf("streamed budget_reached = %#v (ok=%v)", streamed, ok)
	}
	if isRetryableResponsesStreamError(streamed) {
		t.Fatal("streamed budget_reached must not be retried even with retry advice")
	}

	// A 429 with the same code is not the terminal classification Rust maps.
	other := responsesHTTPError("OpenAI", http.StatusTooManyRequests, http.Header{},
		[]byte(`{"error":{"code":"budget_reached","message":"out of budget"}}`))
	if errors.As(other, &apiErr) && apiErr.Kind == codexapi.ErrorSessionBudgetExceeded {
		t.Fatalf("429 budget_reached classified as session budget: %#v", apiErr)
	}
	if !strings.Contains(other.Error(), "out of budget") {
		t.Fatalf("429 error = %v", other)
	}
}
