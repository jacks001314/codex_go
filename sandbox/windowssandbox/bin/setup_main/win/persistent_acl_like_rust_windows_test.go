//go:build windows

package win

import (
	"io"
	"os"
	"path/filepath"
	"testing"
	"unsafe"

	"codex_go/sandbox/windowssandbox"
	"golang.org/x/sys/windows"
)

// TestRepeatedRegisteredSetupKeepsExistingFilePermissionsLikeRust pins Rust
// #60943: ProvisionOnly setup for a registered runtime verifies the persistent
// sandbox directory ACLs and skips rewriting them when they already match, so a
// file moved into the sandbox keeps the permissions it had before the move.
func TestRepeatedRegisteredSetupKeepsExistingFilePermissionsLikeRust(t *testing.T) {
	home := t.TempDir()
	groupSIDBytes, err := windowssandbox.SIDBytesFromString("S-1-5-21-42-43-44-1000")
	if err != nil {
		t.Fatalf("SIDBytesFromString() error = %v", err)
	}
	groupSID, err := windows.StringToSid("S-1-5-21-42-43-44-1000")
	if err != nil {
		t.Fatalf("StringToSid() error = %v", err)
	}
	payload := &setupPayload{
		CodexHome: home,
		RealUser:  setupACLTestRealUser(t),
		Mode:      setupModeProvision,
		Runtime:   string(windowssandbox.SetupRuntimeRegistered),
	}
	if err := lockPersistentSandboxDirs(payload, groupSIDBytes, io.Discard); err != nil {
		t.Fatalf("first lockPersistentSandboxDirs() error = %v", err)
	}
	if !registeredPersistentDirectoriesConfigured(payload, groupSIDBytes) {
		t.Fatal("freshly configured directories did not verify")
	}

	existing := filepath.Join(home, "existing")
	if err := os.WriteFile(existing, []byte("test"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	moved := filepath.Join(windowssandbox.SandboxDir(home), "existing")
	if err := os.Rename(existing, moved); err != nil {
		t.Fatalf("Rename() error = %v", err)
	}
	if aclAllowsSIDMask(t, moved, groupSID, windows.FILE_WRITE_DATA) {
		t.Fatal("moved file is writable by the sandbox group before repeated setup")
	}

	if err := lockPersistentSandboxDirs(payload, groupSIDBytes, io.Discard); err != nil {
		t.Fatalf("second lockPersistentSandboxDirs() error = %v", err)
	}
	if aclAllowsSIDMask(t, moved, groupSID, windows.FILE_WRITE_DATA) {
		t.Fatal("repeated registered setup granted the sandbox group write access to the moved file")
	}

	created := filepath.Join(windowssandbox.SandboxDir(home), "new")
	if err := os.WriteFile(created, []byte("test"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if !aclAllowsSIDMask(t, created, groupSID, windows.FILE_WRITE_DATA) {
		t.Fatal("new file did not inherit sandbox group write access")
	}
}

// TestDirectoryDACLVerificationRequiresExactExplicitPolicyLikeRust covers the
// comparison itself: an extra explicit entry or a changed mask forces a rewrite.
func TestDirectoryDACLVerificationRequiresExactExplicitPolicyLikeRust(t *testing.T) {
	dir := t.TempDir()
	handle, err := windowssandbox.OpenDirectoryNoReparse(
		dir,
		windows.READ_CONTROL|windows.WRITE_DAC,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		windowssandbox.DirectoryOpenExisting,
	)
	if err != nil {
		t.Fatalf("OpenDirectoryNoReparse() error = %v", err)
	}
	defer windows.CloseHandle(handle)
	groupSID, err := windows.StringToSid("S-1-5-21-42-43-44-1000")
	if err != nil {
		t.Fatalf("StringToSid() error = %v", err)
	}
	entry := func(mask uint32) windows.EXPLICIT_ACCESS {
		return windows.EXPLICIT_ACCESS{
			AccessPermissions: windows.ACCESS_MASK(mask),
			AccessMode:        windows.GRANT_ACCESS,
			Inheritance:       windows.SUB_CONTAINERS_AND_OBJECTS_INHERIT,
			Trustee: windows.TRUSTEE{
				TrusteeForm:  windows.TRUSTEE_IS_SID,
				TrusteeType:  windows.TRUSTEE_IS_UNKNOWN,
				TrusteeValue: windows.TrusteeValueFromSID(groupSID),
			},
		}
	}
	expected, err := windows.ACLFromEntries([]windows.EXPLICIT_ACCESS{entry(windows.FILE_GENERIC_READ | windows.FILE_GENERIC_WRITE)}, nil)
	if err != nil {
		t.Fatalf("ACLFromEntries() error = %v", err)
	}
	if err := windows.SetSecurityInfo(handle, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION, nil, nil, expected, nil); err != nil {
		t.Fatalf("SetSecurityInfo() error = %v", err)
	}
	matches, err := directoryDACLMatchesPolicy(handle, expected)
	if err != nil {
		t.Fatalf("directoryDACLMatchesPolicy() error = %v", err)
	}
	if !matches {
		t.Fatal("matching directory DACL was reported as different")
	}
	narrower, err := windows.ACLFromEntries([]windows.EXPLICIT_ACCESS{entry(windows.FILE_GENERIC_READ)}, nil)
	if err != nil {
		t.Fatalf("ACLFromEntries() error = %v", err)
	}
	matches, err = directoryDACLMatchesPolicy(handle, narrower)
	if err != nil {
		t.Fatalf("directoryDACLMatchesPolicy() error = %v", err)
	}
	if matches {
		t.Fatal("changed mask was reported as matching")
	}
}

func setupACLTestRealUser(t *testing.T) string {
	t.Helper()
	token := windows.GetCurrentProcessToken()
	user, err := token.GetTokenUser()
	if err != nil {
		t.Fatalf("GetTokenUser() error = %v", err)
	}
	account, _, _, err := user.User.Sid.LookupAccount("")
	if err != nil {
		t.Skipf("current user is not resolvable: %v", err)
	}
	return account
}

// aclAllowsSIDMask reports whether every requested bit is explicitly allowed
// for the SID and no ACE denies any of them.
func aclAllowsSIDMask(t *testing.T, path string, sid *windows.SID, mask uint32) bool {
	t.Helper()
	descriptor, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatalf("GetNamedSecurityInfo(%s) error = %v", path, err)
	}
	dacl, _, err := descriptor.DACL()
	if err != nil {
		t.Fatalf("DACL(%s) error = %v", path, err)
	}
	if dacl == nil {
		return false
	}
	var allowed, denied uint32
	for index := uint32(0); index < uint32(dacl.AceCount); index++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, index, &ace); err != nil {
			t.Fatalf("GetAce(%s, %d) error = %v", path, index, err)
		}
		if ace == nil || ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE && ace.Header.AceType != windows.ACCESS_DENIED_ACE_TYPE {
			continue
		}
		aceSID := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		if !aceSID.Equals(sid) {
			continue
		}
		if ace.Header.AceType == windows.ACCESS_DENIED_ACE_TYPE {
			denied |= uint32(ace.Mask)
			continue
		}
		allowed |= uint32(ace.Mask)
	}
	return allowed&mask == mask && denied&mask == 0
}
