package idecontext

import (
	"errors"
	"math"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestWindowsPipeConfigTimeoutAndAvailabilityMatchRustCore(t *testing.T) {
	now := time.Unix(100, 0)
	config := WindowsPipeConfig{
		Deadline: now.Add(1500 * time.Millisecond),
	}
	if got := config.PipePath(); got != DefaultWindowsPipeName {
		t.Fatalf("PipePath() = %q, want default", got)
	}
	if got := config.RemainingTimeoutMS(now); got != 1500 {
		t.Fatalf("RemainingTimeoutMS() = %d, want 1500", got)
	}
	if got := RemainingTimeoutMS(now.Add(time.Nanosecond), now); got != 1 {
		t.Fatalf("sub-millisecond timeout = %d, want 1", got)
	}
	if got := RemainingTimeoutMS(now.Add(-time.Second), now); got != 0 {
		t.Fatalf("expired timeout = %d, want 0", got)
	}
	if got := RemainingTimeoutMS(now.Add(time.Duration(math.MaxInt64)), now); got != math.MaxUint32 {
		t.Fatalf("large timeout = %d, want uint32 max", got)
	}

	if WindowsPipeAvailable() != (runtime.GOOS == "windows") {
		t.Fatalf("WindowsPipeAvailable() = %v on %s", WindowsPipeAvailable(), runtime.GOOS)
	}
	if !errors.Is(WindowsPipeTimeoutError(), ErrIDEContextTimedOut) {
		t.Fatalf("WindowsPipeTimeoutError() = %#v", WindowsPipeTimeoutError())
	}
}

func TestValidatePipeServerOwnerMatchRustCore(t *testing.T) {
	if err := ValidatePipeServerOwner("user-a", "user-a"); err != nil {
		t.Fatalf("same owner error = %v", err)
	}
	err := ValidatePipeServerOwner("user-a", "user-b")
	if err == nil || !strings.Contains(err.Error(), "not owned by the current user") {
		t.Fatalf("different owner error = %v", err)
	}
	err = ValidatePipeServerOwner("", "user-b")
	if err == nil || !strings.Contains(err.Error(), "could not be determined") {
		t.Fatalf("missing owner error = %v", err)
	}
}
