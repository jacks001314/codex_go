package windowssandbox

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

// Rust #46237 renders provisioning failures with `{error:#}` so the response and
// the failure log keep every wrapped cause.
func TestSetupErrorChainPreservesNestedCauses(t *testing.T) {
	err := fmt.Errorf("registered sandbox provisioning failed: %w",
		fmt.Errorf("load profile: %w", errors.New("Windows error 5")))
	want := "registered sandbox provisioning failed: load profile: Windows error 5"
	if got := SetupErrorChain(err); got != want {
		t.Fatalf("SetupErrorChain() = %q, want %q", got, want)
	}
	if got := err.Error(); got != want {
		t.Fatalf("err.Error() = %q, want %q", got, want)
	}
}

// Error types that keep their cause out of Error() must still surface it, which
// plain %v formatting drops.
func TestSetupErrorChainSurfacesHiddenCause(t *testing.T) {
	mismatch := &SandboxAccountCredentialMismatch{Underlying: errors.New("CreateProcessWithLogonW failed: 1326")}
	err := fmt.Errorf("provision sandbox users failed: %w", mismatch)
	want := "provision sandbox users failed: Windows rejected the stored sandbox account password: CreateProcessWithLogonW failed: 1326"
	if got := SetupErrorChain(err); got != want {
		t.Fatalf("SetupErrorChain() = %q, want %q", got, want)
	}
	if plain := fmt.Sprintf("%v", err); strings.Contains(plain, "1326") {
		t.Fatalf("plain formatting unexpectedly kept the cause: %q", plain)
	}
}

func TestSetupErrorChainDeduplicatesRepeatedMessages(t *testing.T) {
	err := fmt.Errorf("boom: %w", errors.New("boom"))
	if got, want := SetupErrorChain(err), "boom: boom"; got != want {
		t.Fatalf("SetupErrorChain() = %q, want %q", got, want)
	}
	chain := fmt.Errorf("outer: %w", fmt.Errorf("outer: boom: %w", errors.New("boom")))
	if got, want := SetupErrorChain(chain), "outer: outer: boom: boom"; got != want {
		t.Fatalf("SetupErrorChain() = %q, want %q", got, want)
	}
	wrapped := fmt.Errorf("outer: %w", fmt.Errorf("inner: %w", errors.New("boom")))
	if got, want := SetupErrorChain(wrapped), "outer: inner: boom"; got != want {
		t.Fatalf("SetupErrorChain() = %q, want %q", got, want)
	}
}

func TestSetupErrorChainAllowsNil(t *testing.T) {
	if got := SetupErrorChain(nil); got != "" {
		t.Fatalf("SetupErrorChain(nil) = %q, want empty", got)
	}
}
