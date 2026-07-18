//go:build windows

package windowssandbox

import (
	"os"
	"runtime"
	"testing"
)

const testCapabilitySID = "S-1-5-21-1-2-3-4"

func TestAddDenyReadACEAddsDenyOnce(t *testing.T) {
	path := tempACLFile(t)
	req := ACLRequest{Path: path, SID: testCapabilitySID}

	added, err := addACLACE(req, aclACEKindDenyRead)
	if err != nil {
		t.Fatalf("addACLACE(deny-read) error = %v", err)
	}
	if !added {
		t.Fatalf("addACLACE(deny-read) added = false, want true")
	}
	addedAgain, err := addACLACE(req, aclACEKindDenyRead)
	if err != nil {
		t.Fatalf("addACLACE(deny-read again) error = %v", err)
	}
	if addedAgain {
		t.Fatalf("addACLACE(deny-read again) added = true, want false")
	}

	assertDACLHasACE(t, path, testCapabilitySID, aclACEKindDenyRead)
}

func TestEnsureAllowWriteACEsAddsAllowOnce(t *testing.T) {
	path := t.TempDir()
	req := ACLRequest{Path: path, SID: testCapabilitySID}

	added, err := addACLACE(req, aclACEKindAllowWrite)
	if err != nil {
		t.Fatalf("addACLACE(allow-write) error = %v", err)
	}
	if !added {
		t.Fatalf("addACLACE(allow-write) added = false, want true")
	}
	addedAgain, err := addACLACE(req, aclACEKindAllowWrite)
	if err != nil {
		t.Fatalf("addACLACE(allow-write again) error = %v", err)
	}
	if addedAgain {
		t.Fatalf("addACLACE(allow-write again) added = true, want false")
	}

	assertDACLHasACE(t, path, testCapabilitySID, aclACEKindAllowWrite)
}

func tempACLFile(t *testing.T) string {
	t.Helper()
	file, err := os.CreateTemp(t.TempDir(), "acl-*.txt")
	if err != nil {
		t.Fatalf("CreateTemp() error = %v", err)
	}
	if _, err := file.WriteString("acl test"); err != nil {
		_ = file.Close()
		t.Fatalf("WriteString() error = %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	return file.Name()
}

func assertDACLHasACE(t *testing.T, path string, sidString string, kind aclACEKind) {
	t.Helper()
	sidBytes, err := SIDBytesFromString(sidString)
	if err != nil {
		t.Fatalf("SIDBytesFromString() error = %v", err)
	}
	sid := sidPointerFromBytes(sidBytes)
	sd, dacl, err := fileDACL(path)
	if err != nil {
		t.Fatalf("fileDACL() error = %v", err)
	}
	if !aclAlreadyHasACE(dacl, sid, kind) {
		t.Fatalf("DACL does not contain expected ACE kind %v for %s", kind, sidString)
	}
	runtime.KeepAlive(sidBytes)
	runtime.KeepAlive(sd)
}
