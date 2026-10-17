package agent

import (
	"strings"
	"testing"
)

// TestGuardianInterruptionMessageBoundsReasonAndKeepsGuidanceLikeRust mirrors
// Rust #49312's unit coverage: a long denial message is truncated before the
// Guardian guidance so the parent notification stays within the token limit.
func TestGuardianInterruptionMessageBoundsReasonAndKeepsGuidanceLikeRust(t *testing.T) {
	message, ok := FormatGuardianInterruptionMessage("/root", "/root/worker", strings.Repeat("denied ", 5_000))
	if !ok {
		t.Fatal("FormatGuardianInterruptionMessage() = false")
	}
	if !strings.Contains(message, GuardianNextAction) {
		t.Fatalf("notification lost the Guardian guidance: %.120s", message)
	}
	if !strings.Contains(message, "Agent interrupted by Guardian: ") {
		t.Fatalf("notification lost the interruption prefix: %.120s", message)
	}
	// The reason is bounded by ErrorMaxTokens (four bytes per token) plus the
	// completion envelope; the guidance must not be crowded out.
	if limit := CompletionMessageMaxTokens*4 + 256; len(message) > limit {
		t.Fatalf("notification length = %d, want <= %d", len(message), limit)
	}
}
