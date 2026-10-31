//go:build windows

package windowsandboxservice

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func installCleanupRetryState(t *testing.T, state *ServiceState) {
	t.Helper()
	previous := serviceState.Swap(state)
	t.Cleanup(func() { serviceState.Store(previous) })
}

func stubCleanupRetrySleep(t *testing.T) *int {
	t.Helper()
	sleeps := 0
	previous := cleanupRetrySleep
	cleanupRetrySleep = func(time.Duration) { sleeps++ }
	t.Cleanup(func() { cleanupRetrySleep = previous })
	return &sleeps
}

func TestRetryCleanupReturnsOnSuccess(t *testing.T) {
	installCleanupRetryState(t, &ServiceState{})
	sleeps := stubCleanupRetrySleep(t)
	if err := RetryCleanup(func() error { return nil }); err != nil {
		t.Fatalf("RetryCleanup() error = %v", err)
	}
	if *sleeps != 0 {
		t.Fatalf("RetryCleanup() slept %d times, want 0", *sleeps)
	}
}

func TestRetryCleanupRetriesAfterATransientFailure(t *testing.T) {
	installCleanupRetryState(t, &ServiceState{})
	sleeps := stubCleanupRetrySleep(t)
	attempts := 0
	err := RetryCleanup(func() error {
		attempts++
		if attempts < 3 {
			return errors.New("native cleanup is still running")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("RetryCleanup() error = %v", err)
	}
	if attempts != 3 {
		t.Fatalf("RetryCleanup() attempted %d times, want 3", attempts)
	}
	if *sleeps != 2*cleanupRetryBackoffWaits {
		t.Fatalf("RetryCleanup() slept %d times, want two full backoffs", *sleeps)
	}
}

func TestRetryCleanupStopsAfterFiveStopRequestedFailures(t *testing.T) {
	state := &ServiceState{}
	state.stopRequested.Store(true)
	installCleanupRetryState(t, state)
	sleeps := stubCleanupRetrySleep(t)
	attempts := 0
	err := RetryCleanup(func() error {
		attempts++
		return errors.New("cleanup still failing")
	})
	if err == nil || !strings.Contains(err.Error(), "cleanup still failing") {
		t.Fatalf("RetryCleanup() error = %v", err)
	}
	if attempts != cleanupRetryStopLimit {
		t.Fatalf("RetryCleanup() attempted %d times, want %d", attempts, cleanupRetryStopLimit)
	}
	if *sleeps != cleanupRetryStopLimit-1 {
		t.Fatalf("RetryCleanup() slept %d times, want %d", *sleeps, cleanupRetryStopLimit-1)
	}
}

func TestRetryCleanupReturnsWhenTheServiceShutsDownDuringBackoff(t *testing.T) {
	state := &ServiceState{}
	state.shutdown.Store(true)
	installCleanupRetryState(t, state)
	sleeps := stubCleanupRetrySleep(t)
	err := RetryCleanup(func() error { return errors.New("cleanup still failing") })
	if err == nil {
		t.Fatal("RetryCleanup() succeeded while the service was shutting down")
	}
	if *sleeps != 0 {
		t.Fatalf("RetryCleanup() slept %d times during shutdown, want 0", *sleeps)
	}
}

func TestRetryCleanupWithoutAServiceStateReturnsTheError(t *testing.T) {
	previous := serviceState.Swap(nil)
	t.Cleanup(func() { serviceState.Store(previous) })
	err := RetryCleanup(func() error { return errors.New("cleanup failed") })
	if err == nil || !strings.Contains(err.Error(), "cleanup failed") {
		t.Fatalf("RetryCleanup() error = %v", err)
	}
}
