package codexapi

import (
	"fmt"
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
