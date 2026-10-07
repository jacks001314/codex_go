package codexapi

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestAPIErrorSeparatesDetailsFromRetryDelay(t *testing.T) {
	err := NewAPIErrorWithDetails(APIErrorDetails{
		Kind:    ErrorStream,
		Status:  503,
		Message: "disconnected",
	}).WithRetryDelay(17 * time.Second)

	if details := err.Details(); details.Kind != ErrorStream || details.Status != 503 || details.Message != "disconnected" {
		t.Fatalf("Details() = %#v", details)
	}
	if got, ok := err.RequestedRetryDelay(); !ok || got != 17*time.Second {
		t.Fatalf("RequestedRetryDelay() = %v, %t", got, ok)
	}
}

func TestAPIErrorPreservesExplicitZeroRetryDelay(t *testing.T) {
	err := NewAPIErrorWithDetails(APIErrorDetails{Kind: ErrorServerOverloaded}).
		WithRetryDelay(0)
	if got, ok := RetryDelayInfo(err); !ok || got != 0 {
		t.Fatalf("RetryDelayInfo() = %v, %t", got, ok)
	}
}

func TestRetryDelayTraversesWrappedErrorsAndSupportsAnyDetails(t *testing.T) {
	err := NewAPIErrorWithDetails(APIErrorDetails{
		Kind:    ErrorServerOverloaded,
		Status:  503,
		Message: "busy",
	}).WithRetryDelay(3 * time.Second)

	if got := RetryDelay(fmt.Errorf("request failed: %w", err)); got != 3*time.Second {
		t.Fatalf("RetryDelay() = %v", got)
	}
}

func TestLegacyAPIErrorFieldsRemainCompatible(t *testing.T) {
	err := &APIError{
		Kind:    ErrorRetryable,
		Status:  429,
		Message: "retry later",
		Delay:   2 * time.Second,
	}
	if details := err.Details(); details.Kind != ErrorRetryable || details.Status != 429 || details.Message != "retry later" {
		t.Fatalf("legacy Details() = %#v", details)
	}
	if got := RetryDelay(err); got != 2*time.Second {
		t.Fatalf("legacy RetryDelay() = %v", got)
	}
}

// TestMisalignmentReviewTargetLikeRust covers the ReviewTarget field added to
// the Responses-API misalignment block details by Rust #51217
// ("Preserve review targets and scope misalignment continuation metadata",
// codex-protocol/src/protocol.rs MisalignmentErrorDetails::review_target). The
// opaque target must survive a JSON round trip so the app-server can forward it
// to clients.
func TestMisalignmentReviewTargetLikeRust(t *testing.T) {
	var details MisalignmentDetails
	if err := json.Unmarshal([]byte(`{"errorType":"unauthorized_data_transfer","detailedExplanation":"explain","steer":{"message":"continue"},"reviewTarget":"blk_123"}`), &details); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if details.ReviewTarget == nil || *details.ReviewTarget != "blk_123" {
		t.Fatalf("ReviewTarget = %#v, want blk_123", details.ReviewTarget)
	}
	encoded, err := json.Marshal(details)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	if !strings.Contains(string(encoded), `"reviewTarget":"blk_123"`) {
		t.Fatalf("encoded details = %s, want reviewTarget key", encoded)
	}
}
