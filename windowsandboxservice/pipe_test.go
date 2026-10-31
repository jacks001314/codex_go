package windowsandboxservice

import (
	"strings"
	"testing"
)

func TestPipeSecurityDescriptorDeniesSandboxGroupAndAdmitsInteractiveUsers(t *testing.T) {
	const sandboxSID = "S-1-5-21-1-2-3-1001"
	want := "D:P(D;;GA;;;S-1-5-21-1-2-3-1001)(A;;GA;;;SY)(A;;GA;;;BA)(A;;0x0012019b;;;IU)"
	if got := PipeSecurityDescriptor(sandboxSID); got != want {
		t.Fatalf("PipeSecurityDescriptor() = %q, want %q", got, want)
	}
}

func TestPipeSecurityDescriptorKeepsTheSandboxDenyFirst(t *testing.T) {
	const sandboxSID = "S-1-5-21-9"
	got := PipeSecurityDescriptor(sandboxSID)
	if !strings.HasPrefix(got, "D:P(D;;GA;;;"+sandboxSID+")") {
		t.Fatalf("the sandbox deny ACE must lead the DACL: %q", got)
	}
}
