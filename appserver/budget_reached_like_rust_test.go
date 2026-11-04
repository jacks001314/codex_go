package appserver

import (
	"testing"

	"codex_go/codexapi"
)

// Rust #59228 maps the provider budget stop onto the terminal session budget
// failure instead of the generic bad-request classification.
func TestTurnAnalyticsClassifiesProviderBudgetReachedLikeRust(t *testing.T) {
	fields := turnAnalyticsErrorFieldsFromError(&codexapi.APIError{Kind: codexapi.ErrorSessionBudgetExceeded, Status: 400})
	if fields.TurnError != "sessionBudgetExceeded" {
		t.Fatalf("TurnError = %q, want sessionBudgetExceeded", fields.TurnError)
	}
	if fields.CodexErrorKind == nil || *fields.CodexErrorKind != "session_budget_exceeded" {
		t.Fatalf("CodexErrorKind = %v, want session_budget_exceeded", fields.CodexErrorKind)
	}
	if fields.HTTPStatusCode != nil {
		t.Fatalf("HTTPStatusCode = %v, want nil like Rust", *fields.HTTPStatusCode)
	}
	if got := ErrSessionBudgetExceeded.Error(); got != "budget reached" {
		t.Fatalf("ErrSessionBudgetExceeded = %q, want budget reached", got)
	}
}
