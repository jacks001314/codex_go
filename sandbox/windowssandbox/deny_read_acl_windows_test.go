//go:build windows

package windowssandbox

import (
	"os"
	"runtime"
	"testing"

	"golang.org/x/sys/windows"
)

func TestApplyDenyReadACLsMaterializesMissingPathAndAppliesACE(t *testing.T) {
	path := t.TempDir() + `\missing-secret`
	applied, err := ApplyDenyReadACLs([]string{path}, testCapabilitySID)
	if err != nil {
		t.Fatalf("ApplyDenyReadACLs() error = %v", err)
	}
	if len(applied) == 0 || !containsLexicalPath(applied, path) {
		t.Fatalf("ApplyDenyReadACLs() applied = %#v, want %q", applied, path)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("materialized deny-read path missing: %v", err)
	}
	if !info.IsDir() {
		t.Fatalf("materialized deny-read path is not a directory")
	}

	sidBytes, err := SIDBytesFromString(testCapabilitySID)
	if err != nil {
		t.Fatalf("SIDBytesFromString() error = %v", err)
	}
	sd, dacl, err := fileDACL(path)
	if err != nil {
		t.Fatalf("fileDACL() error = %v", err)
	}
	if !daclHasDeniedMaskForSID(dacl, sidPointerFromBytes(sidBytes), denyReadMask) {
		t.Fatalf("deny-read ACE not present after ApplyDenyReadACLs")
	}
	runtime.KeepAlive(sidBytes)
	runtime.KeepAlive(sd)
}

// TestRevokeACEPreservesChildNullDACLLikeRust mirrors Rust acl_tests
// revoking_absent_sid_preserves_child_null_dacl (#45224): revoking an absent SID
// must not replace a child's null DACL, because an unchanged parent ACL must not
// propagate inheritance into a child that permits it.
func TestRevokeACEPreservesChildNullDACLLikeRust(t *testing.T) {
	parent := t.TempDir()
	child := parent + `\child`
	if err := os.Mkdir(child, 0o700); err != nil {
		t.Fatalf("create child directory: %v", err)
	}
	const inheritedSID = "S-1-5-21-10-20-30-41"
	if _, err := addACLACE(ACLRequest{Path: parent, SID: inheritedSID}, aclACEKindAllowWrite); err != nil {
		t.Fatalf("add inheritable parent ACE: %v", err)
	}
	if err := windows.SetNamedSecurityInfo(child, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.UNPROTECTED_DACL_SECURITY_INFORMATION,
		nil, nil, nil, nil); err != nil {
		t.Skipf("host does not permit a null DACL: %v", err)
	}
	assertNullDACL := func() {
		t.Helper()
		sd, dacl, err := fileDACL(child)
		if err != nil {
			t.Fatalf("fileDACL(child) error = %v", err)
		}
		runtime.KeepAlive(sd)
		if dacl != nil {
			t.Fatal("revocation must preserve the child's null DACL")
		}
	}
	assertNullDACL()

	const absentSID = "S-1-5-21-10-20-30-40"
	if err := RevokeACE(ACLRequest{Path: parent, SID: absentSID}); err != nil {
		t.Fatalf("RevokeACE(absent SID) error = %v", err)
	}
	assertNullDACL()
}

// TestRevokeACEKeepsANullDACL covers the null-DACL short circuit directly: a
// path with no DACL has no ACE to revoke and must not gain an empty one.
func TestRevokeACEKeepsANullDACL(t *testing.T) {
	dir := t.TempDir()
	if err := windows.SetNamedSecurityInfo(dir, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.UNPROTECTED_DACL_SECURITY_INFORMATION,
		nil, nil, nil, nil); err != nil {
		t.Skipf("host does not permit a null DACL: %v", err)
	}
	if err := RevokeACE(ACLRequest{Path: dir, SID: testCapabilitySID}); err != nil {
		t.Fatalf("RevokeACE() error = %v", err)
	}
	sd, dacl, err := fileDACL(dir)
	if err != nil {
		t.Fatalf("fileDACL() error = %v", err)
	}
	runtime.KeepAlive(sd)
	if dacl != nil {
		t.Fatal("a null DACL was replaced by revoking an absent SID")
	}
}
