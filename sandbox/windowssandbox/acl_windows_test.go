//go:build windows

package windowssandbox

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
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

// Mirrors Rust's `runtime_repair_handles_long_directory_and_file_paths`
// (#49058): ACL reads and updates work for a runtime tree nested beyond the
// legacy Windows path limit, a repeat repair is a no-op, and the grant stays
// read/execute - never write, delete or ACL management.
func TestACLRepairHandlesLongDirectoryAndFilePaths(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "dependency-cache")
	for len(directory) <= 280 {
		directory = filepath.Join(directory, strings.Repeat("a", 60))
	}
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatalf("MkdirAll(%s) error = %v", directory, err)
	}
	module := filepath.Join(directory, "module.js")
	if err := os.WriteFile(module, []byte("runtime content"), 0o600); err != nil {
		t.Fatalf("WriteFile(%s) error = %v", module, err)
	}
	short := filepath.Join(root, "short.js")
	if err := os.WriteFile(short, []byte("short-path control"), 0o600); err != nil {
		t.Fatalf("WriteFile(%s) error = %v", short, err)
	}

	readExecute := uint32(windows.FILE_GENERIC_READ | windows.FILE_GENERIC_EXECUTE)
	writeOrACL := uint32(windows.FILE_WRITE_DATA | windows.FILE_APPEND_DATA | windows.DELETE | windows.WRITE_DAC)
	for _, path := range []string{module, short, directory} {
		allows, err := PathMaskAllows(ACLRequest{Path: path, SID: testCapabilitySID, Mask: readExecute}, true)
		if err != nil {
			t.Fatalf("PathMaskAllows(%s) error = %v", path, err)
		}
		if allows {
			t.Fatalf("PathMaskAllows(%s) = true before repair, want false", path)
		}
	}
	// Exercise the write for the nested file too: a parent grant during
	// traversal could otherwise make its inherited ACL a read-only no-op.
	for _, path := range []string{directory, module, short} {
		changed, err := addAllowMaskACE(ACLRequest{Path: path, SID: testCapabilitySID, Mask: readExecute}, 0)
		if err != nil {
			t.Fatalf("addAllowMaskACE(%s) error = %v", path, err)
		}
		if !changed {
			t.Fatalf("addAllowMaskACE(%s) changed = false, want true", path)
		}
	}
	for attempt := 0; attempt < 2; attempt++ {
		for _, path := range []string{directory, module, short} {
			changed, err := addAllowMaskACE(ACLRequest{Path: path, SID: testCapabilitySID, Mask: readExecute}, 0)
			if err != nil {
				t.Fatalf("addAllowMaskACE(%s, attempt %d) error = %v", path, attempt, err)
			}
			if changed {
				t.Fatalf("addAllowMaskACE(%s, attempt %d) changed = true, want false", path, attempt)
			}
			allows, err := PathMaskAllows(ACLRequest{Path: path, SID: testCapabilitySID, Mask: readExecute}, true)
			if err != nil {
				t.Fatalf("PathMaskAllows(%s) error = %v", path, err)
			}
			if !allows {
				t.Fatalf("PathMaskAllows(%s) = false after repair, want true", path)
			}
			hasWriteOrACL, err := PathMaskAllows(ACLRequest{Path: path, SID: testCapabilitySID, Mask: writeOrACL}, false)
			if err != nil {
				t.Fatalf("PathMaskAllows(%s, write) error = %v", path, err)
			}
			if hasWriteOrACL {
				t.Fatalf("PathMaskAllows(%s, write/delete/ACL) = true, want false", path)
			}
		}
	}
}
