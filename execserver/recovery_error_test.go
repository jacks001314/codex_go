package execserver

import (
	"context"
	"errors"
	"fmt"
	"io"
	"testing"
)

func TestIsRetryableRecoveryError(t *testing.T) {
	if IsRetryableRecoveryError(nil) {
		t.Fatal("nil error reported retryable")
	}
	if !IsRetryableRecoveryError(&clientTransportError{err: errors.New("closed")}) {
		t.Fatal("transport error should be retryable")
	}
	if !IsRetryableRecoveryError(errors.Join(errors.New("wrapped"), &clientTransportError{err: errors.New("closed")})) {
		t.Fatal("wrapped transport error should be retryable")
	}
	if IsRetryableRecoveryError(errors.New("protocol")) {
		t.Fatal("non-transport error should not be retryable")
	}
	if !IsRetryableRecoveryError(fmt.Errorf("wrapped: %w", &clientTransportError{err: errors.New("closed")})) {
		t.Fatal("fmt-wrapped transport error should be retryable")
	}
	if IsRetryableRecoveryError(context.Canceled) {
		t.Fatal("context cancellation should not be retryable")
	}
	if IsRetryableRecoveryError(io.EOF) {
		t.Fatal("EOF should not be retryable")
	}
}
