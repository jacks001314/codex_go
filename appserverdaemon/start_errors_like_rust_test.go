package appserverdaemon

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

// Rust #59058 classifies auto-start failures from the innermost typed error and
// keeps the original diagnostic for the caller.
func TestStartFailureClassificationLikeRust(t *testing.T) {
	for _, test := range []struct {
		name   string
		reason StartFailureReason
	}{
		{"install", StartFailureInstall},
		{"spawn", StartFailureSpawn},
		{"early child exit", StartFailureEarlyChildExit},
		{"readiness timeout", StartFailureReadinessTimeout},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := wrapStartError(test.reason, errors.New("boom"))
			if err == nil || !strings.Contains(err.Error(), "boom") {
				t.Fatalf("classified error lost its diagnostic: %v", err)
			}
			if got := StartFailureReasonOf(fmt.Errorf("context: %w", err)); got != test.reason {
				t.Fatalf("reason = %q, want %q", got, test.reason)
			}
		})
	}
	if got := StartFailureReasonOf(errors.New("plain")); got != StartFailureOther {
		t.Fatalf("unclassified reason = %q, want other", got)
	}
	inner := wrapStartError(StartFailureSpawn, errors.New("spawn failed"))
	outer := wrapStartError(StartFailureInstall, inner)
	if got := StartFailureReasonOf(outer); got != StartFailureSpawn {
		t.Fatalf("outer wrapper replaced the innermost reason: %q", got)
	}
	if wrapped := wrapStartError(StartFailureSpawn, nil); wrapped != nil {
		t.Fatalf("nil error must stay nil, got %v", wrapped)
	}
}
