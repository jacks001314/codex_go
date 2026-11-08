package analytics

import (
	"fmt"
	"net/http"
	"testing"

	"codex_go/chatgptapi"
	"codex_go/runtimeauth"
)

// TestAnalyticsRuntimeAuthErrorsKeepSignInGuidanceLikeRust pins Rust #60101: a
// runtime credential failure is a sign-in problem, so the dashboard keeps its
// "sign in again" guidance instead of reporting a generic request failure.
func TestAnalyticsRuntimeAuthErrorsKeepSignInGuidanceLikeRust(t *testing.T) {
	for _, failure := range []error{
		runtimeauth.ErrUnavailable,
		runtimeauth.ErrRefreshFailed,
		runtimeauth.ErrUnsupported,
	} {
		wrapped := fmt.Errorf("analytics request: %w", failure)
		if !isUnauthorizedAnalyticsError(wrapped) {
			t.Fatalf("%v did not report a sign-in problem", failure)
		}
		if got := analyticsRequestErrorMessage(wrapped); got != analyticsReportSignInMessage {
			t.Fatalf("report message for %v = %q, want %q", failure, got, analyticsReportSignInMessage)
		}
	}

	// A transient credential change must stay a retryable failure, not sign-in
	// guidance: only the caller can decide to retry.
	if isUnauthorizedAnalyticsError(runtimeauth.ErrChanged) {
		t.Fatal("a credential change reported a sign-in problem")
	}

	// Ordinary HTTP mapping is unchanged.
	unauthorized := &chatgptapi.HTTPStatusError{StatusCode: http.StatusUnauthorized}
	if !isUnauthorizedAnalyticsError(unauthorized) {
		t.Fatal("a 401 did not report a sign-in problem")
	}
	forbidden := &chatgptapi.HTTPStatusError{StatusCode: http.StatusForbidden}
	if isUnauthorizedAnalyticsError(forbidden) {
		t.Fatal("a 403 reported a sign-in problem")
	}
}
