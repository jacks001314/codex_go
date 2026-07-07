//go:build !windows

package windowssandbox

import "testing"

func TestPlatformStubsAreExplicitUnsupported(t *testing.T) {
	if err := AddDenyReadACE(ACLRequest{Path: "/tmp/file", SID: "S-1-5-21-1-2-3-4"}); !IsUnsupported(err) {
		t.Fatalf("AddDenyReadACE() error = %v, want unsupported", err)
	}
	if err := AddDenyWriteACE(ACLRequest{Path: "/tmp/file", SID: "S-1-5-21-1-2-3-4"}); !IsUnsupported(err) {
		t.Fatalf("AddDenyWriteACE() error = %v, want unsupported", err)
	}
	if err := EnsureAllowWriteACEs(ACLRequest{Path: "/tmp/file", SID: "S-1-5-21-1-2-3-4"}); !IsUnsupported(err) {
		t.Fatalf("EnsureAllowWriteACEs() error = %v, want unsupported", err)
	}
	if err := AllowNullDevice("S-1-5-21-1-2-3-4"); !IsUnsupported(err) {
		t.Fatalf("AllowNullDevice() error = %v, want unsupported", err)
	}
	if _, err := ApplyDenyReadACLs([]string{"/tmp/secret"}, "S-1-5-21-1-2-3-4"); !IsUnsupported(err) {
		t.Fatalf("ApplyDenyReadACLs() error = %v, want unsupported", err)
	}
	if _, err := SyncPersistentDenyReadACLs("/tmp/codex-home", []string{"/tmp/secret"}, "S-1-5-21-1-2-3-4"); !IsUnsupported(err) {
		t.Fatalf("SyncPersistentDenyReadACLs() error = %v, want unsupported", err)
	}
	if _, err := PrepareLaunchDesktop(true); !IsUnsupported(err) {
		t.Fatalf("PrepareLaunchDesktop(true) error = %v, want unsupported", err)
	}
	if _, err := AuditEveryoneWritable("/tmp", nil, ""); !IsUnsupported(err) {
		t.Fatalf("AuditEveryoneWritable() error = %v, want unsupported", err)
	}
	if _, err := ApplyWorldWritableScanAndDenies(&WorldWritableAuditRequest{CWD: "/tmp"}); !IsUnsupported(err) {
		t.Fatalf("ApplyWorldWritableScanAndDenies() error = %v, want unsupported", err)
	}
	if _, err := NewProcThreadAttributeList(); !IsUnsupported(err) {
		t.Fatalf("NewProcThreadAttributeList() error = %v, want unsupported", err)
	}
	if _, err := DPAPIProtect([]byte("secret")); !IsUnsupported(err) {
		t.Fatalf("DPAPIProtect() error = %v, want unsupported", err)
	}
	if _, err := DPAPIUnprotect([]byte("secret")); !IsUnsupported(err) {
		t.Fatalf("DPAPIUnprotect() error = %v, want unsupported", err)
	}
	if _, err := ConvertStringSIDToSID("S-1-5-32-544"); !IsUnsupported(err) {
		t.Fatalf("ConvertStringSIDToSID() error = %v, want unsupported", err)
	}
	if _, err := SIDBytesFromString("S-1-5-32-544"); !IsUnsupported(err) {
		t.Fatalf("SIDBytesFromString() error = %v, want unsupported", err)
	}
	if _, err := GetCurrentTokenForRestriction(); !IsUnsupported(err) {
		t.Fatalf("GetCurrentTokenForRestriction() error = %v, want unsupported", err)
	}
	if _, err := CreateReadonlyTokenWithCapsFrom(1, []string{"S-1-5-21-1-2-3-4"}); !IsUnsupported(err) {
		t.Fatalf("CreateReadonlyTokenWithCapsFrom() error = %v, want unsupported", err)
	}
	if _, err := CreateWorkspaceWriteTokenWithCapsFrom(1, []string{"S-1-5-21-1-2-3-4"}); !IsUnsupported(err) {
		t.Fatalf("CreateWorkspaceWriteTokenWithCapsFrom() error = %v, want unsupported", err)
	}
	if err := CloseTokenHandle(1); !IsUnsupported(err) {
		t.Fatalf("CloseTokenHandle(1) error = %v, want unsupported", err)
	}
}
