//go:build windows

package windowsandboxservice

import (
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Rust harden_service_dacl (#62122) installs an explicit DACL on the sandbox
// service: SYSTEM and the built-in administrators keep full control, and other
// authenticated users keep only the query, start, and interrogate rights that
// sandbox setup needs.
func TestSandboxServiceDACLMatchesRustLikeRust(t *testing.T) {
	descriptor, err := windows.SecurityDescriptorFromString(sandboxServiceDACL)
	if err != nil {
		t.Fatalf("SecurityDescriptorFromString(%q) error = %v", sandboxServiceDACL, err)
	}
	dacl, _, err := descriptor.DACL()
	if err != nil || dacl == nil {
		t.Fatalf("DACL() = %v, %v; want a DACL", dacl, err)
	}
	// GA stays GENERIC_ALL in the descriptor form and the service manager maps it to
	// SERVICE_ALL_ACCESS; CCLCSWRPLORC is query config, query status, enumerate
	// dependents, start, interrogate, and READ_CONTROL.
	want := []struct {
		sid  string
		mask uint32
	}{
		{"S-1-5-18", 0x10000000},
		{"S-1-5-32-544", 0x10000000},
		{"S-1-5-11", 0x2009d},
	}
	for index, entry := range want {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, uint32(index), &ace); err != nil || ace == nil {
			t.Fatalf("GetAce(%d) = %v, %v", index, ace, err)
		}
		if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE {
			t.Fatalf("ace %d type = %d, want an allowed ACE", index, ace.Header.AceType)
		}
		if uint32(ace.Mask) != entry.mask {
			t.Fatalf("ace %d mask = %#x, want %#x", index, uint32(ace.Mask), entry.mask)
		}
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		if got := sid.String(); got != entry.sid {
			t.Fatalf("ace %d trustee = %s, want %s", index, got, entry.sid)
		}
	}
}
