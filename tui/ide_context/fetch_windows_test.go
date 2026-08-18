//go:build windows

package idecontext

import (
	"testing"

	"golang.org/x/sys/windows"
)

// TestWindowsPipeOpensWithIdentificationOnlyImpersonationLikeRust pins the
// SECURITY_SQOS_PRESENT | SECURITY_IDENTIFICATION flags applied to the
// IDE-context named-pipe CreateFile call (Rust #39020).
func TestWindowsPipeOpensWithIdentificationOnlyImpersonationLikeRust(t *testing.T) {
	if windowsPipeSecurityFlags&windows.SECURITY_SQOS_PRESENT == 0 {
		t.Fatal("windowsPipeSecurityFlags lacks SECURITY_SQOS_PRESENT")
	}
	if windowsPipeSecurityFlags&windows.SECURITY_IDENTIFICATION == 0 {
		t.Fatal("windowsPipeSecurityFlags lacks SECURITY_IDENTIFICATION")
	}
	if windowsPipeSecurityFlags&^(windows.SECURITY_SQOS_PRESENT|windows.SECURITY_IDENTIFICATION) != 0 {
		t.Fatalf("windowsPipeSecurityFlags carries unexpected bits: %#x", windowsPipeSecurityFlags)
	}
}
