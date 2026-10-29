package windowssandbox

import (
	"strings"
	"testing"
)

// TestPackageIdentityDecisionLikeRust pins Rust #46575's policy: the caller's
// OS package identity alone decides whether desktop app context is preserved,
// an unpackaged caller continues without it, and a registered-core launch
// without an identity fails instead of silently dropping the context.
func TestPackageIdentityDecisionLikeRust(t *testing.T) {
	preserve, err := PackageIdentityDecision(true, false)
	if err != nil || !preserve {
		t.Fatalf("identity without registered core = (%t, %v), want (true, nil)", preserve, err)
	}
	preserve, err = PackageIdentityDecision(true, true)
	if err != nil || !preserve {
		t.Fatalf("identity with registered core = (%t, %v), want (true, nil)", preserve, err)
	}
	preserve, err = PackageIdentityDecision(false, false)
	if err != nil || preserve {
		t.Fatalf("unpackaged caller = (%t, %v), want (false, nil)", preserve, err)
	}
	preserve, err = PackageIdentityDecision(false, true)
	if err == nil || !strings.Contains(err.Error(), "registered Core process has no package identity") {
		t.Fatalf("registered core without identity = (%t, %v)", preserve, err)
	}
}
