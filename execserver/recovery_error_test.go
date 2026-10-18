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

func TestIsRetryablePreparationErrorByTypeLikeRust(t *testing.T) {
	unavailable := fmt.Errorf("wrapped registry: %w", &remoteRegistryHTTPError{
		StatusCode: 503,
		message:    "service unavailable",
	})
	if !IsRetryablePreparationError(unavailable) {
		t.Fatal("typed registry service-unavailable error was not retryable")
	}
	mapped := preparationReadError(unavailable)
	if !errors.Is(mapped, io.ErrClosedPipe) || !errors.Is(mapped, unavailable) {
		t.Fatalf("mapped preparation error = %v", mapped)
	}
	if !IsRetryablePreparationError(&clientTransportError{err: errors.New("closed")}) {
		t.Fatal("transport failure was not retryable")
	}
	protocol := errors.New("protocol error: transport closed")
	if IsRetryablePreparationError(protocol) {
		t.Fatal("protocol text containing transport-closed was classified as retryable")
	}
	if errors.Is(preparationReadError(protocol), io.ErrClosedPipe) {
		t.Fatal("protocol error was mapped to BrokenPipe")
	}
}
