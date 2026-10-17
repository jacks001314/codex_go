package codexapi

import (
	"fmt"
	"testing"
	"time"
)

func TestRetryAfterPreservesCapturedDeadlineAcrossErrorPropagation(t *testing.T) {
	now := time.Now()
	deadline := now.Add(10 * time.Second)
	err := NewAPIErrorWithDetails(APIErrorDetails{Kind: ErrorRetryable, Message: "busy"}).
		WithRetryAfter(NewRetryAfterAt(deadline))
	wrapped := fmt.Errorf("outer: %w", err)

	retryAfter, ok := RetryAfterInfo(wrapped)
	if !ok || !retryAfter.Deadline().Equal(deadline) {
		t.Fatalf("RetryAfterInfo() = %v, %t; want %v", retryAfter.Deadline(), ok, deadline)
	}
	if got := retryAfter.remainingDelayAt(now.Add(4 * time.Second)); got != 6*time.Second {
		t.Fatalf("remaining delay after 4s = %v, want 6s", got)
	}
	if got := retryAfter.remainingDelayAt(now.Add(11 * time.Second)); got != 0 {
		t.Fatalf("expired remaining delay = %v, want zero", got)
	}
}

func TestExpiredRetryAfterRemainsExplicitAdvice(t *testing.T) {
	err := NewAPIErrorWithDetails(APIErrorDetails{Kind: ErrorRetryable, Message: "busy"}).
		WithRetryAfter(NewRetryAfterAt(time.Now().Add(-time.Second)))
	delay, ok := RetryDelayInfo(fmt.Errorf("wrapped: %w", err))
	if !ok || delay != 0 {
		t.Fatalf("expired RetryDelayInfo() = %v, %t; want explicit zero advice", delay, ok)
	}
}

func TestWithRetryDelayCapturesDeadlineCompatibility(t *testing.T) {
	err := NewAPIErrorWithDetails(APIErrorDetails{Kind: ErrorRetryable}).WithRetryDelay(2 * time.Second)
	retryAfter, ok := RetryAfterInfo(err)
	if !ok {
		t.Fatal("WithRetryDelay did not capture retry advice")
	}
	delay := retryAfter.RemainingDelay()
	if delay <= 0 || delay > 2*time.Second {
		t.Fatalf("remaining compatibility delay = %v, want (0, 2s]", delay)
	}
}
