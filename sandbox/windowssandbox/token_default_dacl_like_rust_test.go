//go:build windows

package windowssandbox

import (
	"errors"
	"runtime"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

type tokenDefaultDACLEntry struct {
	sid  string
	mask windows.ACCESS_MASK
}

// TestDefaultObjectsRestrictAccessToLogonSessionLikeRust checks Rust #47361:
// the runner token's default DACL grants full access to its own logon SID and
// only READ_CONTROL to OWNER RIGHTS, so a shared account owner (or another
// logon of the same account) cannot rewrite the DACL or reach the runner's
// processes, threads and IPC objects.
func TestDefaultObjectsRestrictAccessToLogonSessionLikeRust(t *testing.T) {
	baseToken, err := GetCurrentTokenForRestriction()
	if err != nil {
		t.Fatalf("GetCurrentTokenForRestriction() error = %v", err)
	}
	defer CloseTokenHandle(baseToken)

	restricted, err := CreateWorkspaceWriteTokenWithCapsFrom(baseToken, []string{"S-1-5-21-10-20-30-40"})
	if err != nil {
		if errors.Is(err, ErrHostUnsupported) || errors.Is(err, windows.ERROR_INVALID_PARAMETER) {
			t.Skipf("host rejected WRITE_RESTRICTED CreateRestrictedToken: %v", err)
		}
		t.Fatalf("CreateWorkspaceWriteTokenWithCapsFrom() error = %v", err)
	}
	defer CloseTokenHandle(restricted)

	logonSID, err := getLogonSIDBytes(windows.Token(baseToken))
	if err != nil {
		t.Fatalf("getLogonSIDBytes() error = %v", err)
	}
	logonSIDString := StringFromSIDBytes(logonSID)

	entries := tokenDefaultDACLEntries(t, windows.Token(restricted))
	if len(entries) != 2 {
		t.Fatalf("default DACL has %d entries, want 2: %+v", len(entries), entries)
	}
	masks := make(map[string]windows.ACCESS_MASK, len(entries))
	for _, entry := range entries {
		masks[entry.sid] = entry.mask
	}
	if got := masks[logonSIDString]; got != windows.GENERIC_ALL {
		t.Fatalf("logon SID %s mask = %#x, want GENERIC_ALL: %+v", logonSIDString, got, entries)
	}
	if got, ok := masks["S-1-3-4"]; !ok || got != windows.READ_CONTROL {
		t.Fatalf("OWNER RIGHTS mask = %#x (present=%v), want READ_CONTROL: %+v", got, ok, entries)
	}
	if _, ok := masks["S-1-1-0"]; ok {
		t.Fatalf("default DACL must not grant Everyone: %+v", entries)
	}
	if _, ok := masks["S-1-5-21-10-20-30-40"]; ok {
		t.Fatalf("default DACL must not grant the filesystem capability: %+v", entries)
	}
}

func tokenDefaultDACLEntries(t *testing.T, token windows.Token) []tokenDefaultDACLEntry {
	t.Helper()
	var needed uint32
	_ = windows.GetTokenInformation(token, windows.TokenDefaultDacl, nil, 0, &needed)
	if needed == 0 {
		t.Fatalf("GetTokenInformation(TokenDefaultDacl) returned no size")
	}
	buffer := make([]byte, needed)
	if err := windows.GetTokenInformation(token, windows.TokenDefaultDacl, &buffer[0], needed, &needed); err != nil {
		t.Fatalf("GetTokenInformation(TokenDefaultDacl) error = %v", err)
	}
	info := (*tokenDefaultDACLInfo)(unsafe.Pointer(&buffer[0]))
	if info.DefaultDACL == nil {
		t.Fatalf("token has no default DACL")
	}
	count := uint32(info.DefaultDACL.AceCount)
	entries := make([]tokenDefaultDACLEntry, 0, count)
	for index := uint32(0); index < count; index++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(info.DefaultDACL, index, &ace); err != nil {
			t.Fatalf("GetAce(%d) error = %v", index, err)
		}
		if ace == nil || ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE {
			t.Fatalf("ACE %d is not an access-allowed ACE", index)
		}
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		if !sid.IsValid() {
			t.Fatalf("ACE %d has an invalid SID", index)
		}
		entries = append(entries, tokenDefaultDACLEntry{sid: sid.String(), mask: ace.Mask})
	}
	runtime.KeepAlive(buffer)
	return entries
}
